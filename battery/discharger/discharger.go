package discharger

import (
	"errors"
	"fmt"
	"gok-pi/battery/entity"
	"gok-pi/internal/lib/sl"
	"gok-pi/internal/lib/timer"
	"gok-pi/metrics/observers"
	"log/slog"
	"time"
)

type Client interface {
	Status() (*entity.SystemStatus, error)
	StartDischarge(power int) error
	StopDischarge() error
	SwitchOperatingModeToManual(currentMode string) error
	SwitchOperatingModeToAuto(currentMode string) error
}

type CommandType string

const (
	CommandStartDischarge CommandType = "start_discharge"
	CommandStopDischarge  CommandType = "stop_discharge"
	CommandSetLimits      CommandType = "set_limits"
	CommandForceMode      CommandType = "force_mode"
)

type OperatingMode string

const (
	OperatingModeManual OperatingMode = "manual"
	OperatingModeAuto   OperatingMode = "auto"
)

type ControlCommand struct {
	Type   CommandType
	Power  int
	Limits *CommandLimits
	Mode   OperatingMode
}

type CommandLimits struct {
	PowerLimit *int
	SocLimit   *int
}

var ErrCommandQueueFull = errors.New("discharger command queue full")

type Discharge struct {
	name             string
	schedules        []entity.Schedule
	capacityLimit    float64 // Capacity limit in Wh calculated based on the SoC limit
	powerLimit       int
	socLimit         float64
	readyToDischarge bool
	isDischarging    bool
	soc              float64 // State of Charge from last status
	capacity         float64 // Remaining capacity in Wh from last status
	stopTime         time.Time
	rate             int // Discharge rate in Wh/h calculated based on the remaining capacity and time
	client           Client
	status           *entity.SystemStatus
	log              *slog.Logger
	commands         chan ControlCommand
	manualOverride   bool
}

func New(name string, client Client, log *slog.Logger) (*Discharge, error) {
	return &Discharge{
		name:     name,
		client:   client,
		log:      log.With(sl.Module("battery.discharge")),
		commands: make(chan ControlCommand, 16),
	}, nil
}

func (d *Discharge) SetCapacityLimit(_ int) {
	//d.capacityLimit = float64(capacityLimit)
}

func (d *Discharge) SetLimits(powerLimit, socLimit int) {
	d.powerLimit = powerLimit
	d.socLimit = float64(socLimit)
}

func (d *Discharge) AddSchedule(schedule entity.Schedule) {
	d.schedules = append(d.schedules, schedule)
}

func (d *Discharge) SubmitCommand(cmd ControlCommand) error {
	select {
	case d.commands <- cmd:
		return nil
	default:
		return ErrCommandQueueFull
	}
}

func (d *Discharge) Run() error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case cmd := <-d.commands:
			if err := d.processControlCommand(cmd); err != nil {
				d.log.With(sl.Err(err)).Error("processing remote command")
			}
		case <-ticker.C:
			status, err := d.client.Status()
			if err != nil {
				d.log.With(sl.Err(err)).Error("checking battery status")
				continue
			}
			d.observeStatus(status)

			if len(d.schedules) == 0 {
				if !d.manualOverride {
					continue
				}
			}

			if d.manualOverride {
				d.readyToDischarge = true
			} else {
				d.checkTime()
			}
			if d.readyToDischarge {
				d.runDischarge()
			} else {
				err = d.stopDischarge()
				if err != nil {
					d.log.With(sl.Err(err)).Error("stopping discharge")
				}
			}
		}
	}
}

// stopCondition checks if the current state of charge (SoC) is below the specified limit.
func (d *Discharge) stopCondition() bool {
	return d.socLimit >= d.soc
}

// isTimeToDischarge determines whether the current time falls within the specified discharge time window.
func (d *Discharge) isTimeToDischarge(start, stop string) bool {
	// Calculate the start and stop times for today
	startTime, err := timer.ParseTime(start)
	if err != nil {
		d.log.With(sl.Err(err)).Error("parsing start time")
		return false
	}
	stopTime, err := timer.ParseTime(stop)
	if err != nil {
		d.log.With(sl.Err(err)).Error("parsing stop time")
		return false
	}
	if startTime.After(stopTime) {
		stopTime = stopTime.Add(24 * time.Hour)
	}
	d.stopTime = stopTime
	now := time.Now()
	return now.After(startTime) && now.Before(stopTime)
}

// checkTime determines whether the current time falls within the specified discharge time window.
func (d *Discharge) checkTime() {

	for _, schedule := range d.schedules {
		if schedule.Enabled {
			if d.isTimeToDischarge(schedule.StartTime, schedule.StopTime) {
				d.SetLimits(schedule.PowerLimit, schedule.SocLimit)
				d.calculateRate()
				d.readyToDischarge = true
				return
			}
		}
	}

	d.readyToDischarge = false
}

// runDischarge manages the discharge process of the battery based on its current status and predefined limits.
func (d *Discharge) runDischarge() {
	if d.status == nil {
		return
	}
	log := d.log.With(
		slog.String("operating_mode", d.status.OperatingMode),
		slog.Float64("remaining capacity", d.status.RemainingCapacityWh),
		slog.Float64("SoC", d.status.RSOC),
		slog.Int("rate", d.rate),
		slog.Float64("consumption", d.status.ConsumptionW),
		slog.Bool("discharge", d.status.BatteryDischarging),
	)

	if d.isDischarging {
		if d.stopCondition() {
			log.Info("battery level reached the limit, stopping discharge")
			err := d.stopDischarge()
			if err != nil {
				d.log.With(sl.Err(err)).Error("stopping discharge")
				return
			}
		}
		return
	}

	if d.rate == 0 && !d.isDischarging {
		return
	}

	err := d.client.SwitchOperatingModeToManual(d.status.OperatingMode)
	if err != nil {
		d.log.With(sl.Err(err)).Error("switching operating mode")
		return
	}

	log.Info("starting discharge")
	err = d.client.StartDischarge(d.rate)
	if err != nil {
		d.log.With(sl.Err(err)).Error("starting discharge")
		return
	}
	d.isDischarging = true

}

// stopDischarge stops the current discharge activity if it is ongoing.
// Returns an error if the operation fails at any point.
func (d *Discharge) stopDischarge() error {
	if d.isDischarging {

		err := d.client.StopDischarge()
		if err != nil {
			return err
		}

		if d.status != nil {
			err = d.client.SwitchOperatingModeToAuto(d.status.OperatingMode)
			if err != nil {
				return err
			}
		}

		d.isDischarging = false
	}
	return nil
}

func (d *Discharge) processControlCommand(cmd ControlCommand) error {
	log := d.log.With(
		slog.String("command", string(cmd.Type)),
	)

	switch cmd.Type {
	case CommandStartDischarge:
		if cmd.Power > 0 {
			d.rate = cmd.Power
		}
		d.manualOverride = true
		d.readyToDischarge = true

		currentMode := ""
		if d.status != nil {
			currentMode = d.status.OperatingMode
		}

		if err := d.client.SwitchOperatingModeToManual(currentMode); err != nil {
			return fmt.Errorf("switching to manual mode: %w", err)
		}

		if d.rate <= 0 {
			if d.powerLimit > 0 {
				d.rate = d.powerLimit
			} else {
				return fmt.Errorf("no discharge rate configured")
			}
		}

		log.With(slog.Int("rate", d.rate)).Info("starting discharge via remote command")
		if err := d.client.StartDischarge(d.rate); err != nil {
			return fmt.Errorf("starting discharge: %w", err)
		}

		d.isDischarging = true
		return nil

	case CommandStopDischarge:
		d.manualOverride = false
		log.Info("stopping discharge via remote command")
		return d.stopDischarge()

	case CommandSetLimits:
		if cmd.Limits == nil {
			return fmt.Errorf("missing limits payload")
		}
		if cmd.Limits.PowerLimit != nil {
			d.powerLimit = *cmd.Limits.PowerLimit
			log = log.With(slog.Int("power_limit", d.powerLimit))
		}
		if cmd.Limits.SocLimit != nil {
			d.socLimit = float64(*cmd.Limits.SocLimit)
			log = log.With(slog.Int("soc_limit", *cmd.Limits.SocLimit))
		}
		d.calculateRate()
		log.With(slog.Int("rate", d.rate)).Info("updated discharge limits via remote command")

		if d.manualOverride && d.rate > 0 {
			if err := d.client.StartDischarge(d.rate); err != nil {
				return fmt.Errorf("applying updated rate: %w", err)
			}
			d.isDischarging = true
		}
		return nil

	case CommandForceMode:
		currentMode := ""
		if d.status != nil {
			currentMode = d.status.OperatingMode
		}
		switch cmd.Mode {
		case OperatingModeManual:
			d.manualOverride = true
			if err := d.client.SwitchOperatingModeToManual(currentMode); err != nil {
				return fmt.Errorf("forcing manual mode: %w", err)
			}
			log.Info("forced manual mode via remote command")
			return nil
		case OperatingModeAuto:
			d.manualOverride = false
			if err := d.client.SwitchOperatingModeToAuto(currentMode); err != nil {
				return fmt.Errorf("forcing auto mode: %w", err)
			}
			log.Info("forced auto mode via remote command")
			return nil
		default:
			return fmt.Errorf("unknown operating mode: %s", cmd.Mode)
		}
	default:
		return fmt.Errorf("unsupported command type: %s", cmd.Type)
	}
}

// calculate discharge rate as Wh/h
func (d *Discharge) calculateRate() {
	d.rate = 0
	estimate := d.capacity - d.capacityLimit
	if estimate <= 0 {
		return
	}
	remainingTime := d.stopTime.Sub(time.Now())
	if remainingTime <= 0 {
		return
	}
	rate := estimate / remainingTime.Hours()
	if rate <= float64(d.powerLimit) {
		d.rate = int(rate)
	} else {
		d.rate = d.powerLimit
	}
}

// observeStatus updates various battery status metrics through external observers.
// If the status is nil, the method returns immediately.
func (d *Discharge) observeStatus(status *entity.SystemStatus) {
	if status == nil {
		return
	}

	d.status = status
	d.soc = status.USOC
	d.capacity = status.RemainingCapacityWh
	d.capacityLimit = 0
	if status.RSOC > 0 {
		d.capacityLimit = d.socLimit * status.RemainingCapacityWh / status.RSOC
	}

	go func(status *entity.SystemStatus) {
		observers.UpdateSoC(d.name, status.RSOC)
		observers.UpdateUSoC(d.name, status.USOC)
		observers.UpdateCapacity(d.name, status.RemainingCapacityWh)
		observers.UpdateConsumption(d.name, status.ConsumptionW)
		observers.UpdatePac(d.name, status.PacTotalW)
		observers.UpdateDischargeState(d.name, status.BatteryDischarging)
		observers.UpdateOpMode(d.name, status.OperatingMode)
	}(status)
}
