package charger

import (
	"errors"
	"fmt"
	"gok-pi/battery/entity"
	"gok-pi/internal/lib/sl"
	"gok-pi/internal/lib/timer"
	"gok-pi/metrics/observers"
	"log/slog"
	"sync"
	"time"
)

type Client interface {
	Status() (*entity.SystemStatus, error)
	StartCharge(power int) error
	StopCharge() error
	SwitchOperatingModeToManual(currentMode string) error
	SwitchOperatingModeToAuto(currentMode string) error
}

type CommandType string

const (
	CommandStartCharge  CommandType = "start_charge"
	CommandStopCharge   CommandType = "stop_charge"
	CommandSetLimits    CommandType = "set_limits"
	CommandForceMode    CommandType = "force_mode"
	CommandUpdateConfig CommandType = "update_config"
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
	Config *ConfigUpdate
}

type CommandLimits struct {
	PowerLimit *int
	SocLimit   *int
}

type ConfigUpdate struct {
	Schedules  []entity.Schedule
	PowerLimit *int
	SocLimit   *int
}

var ErrCommandQueueFull = errors.New("charger command queue full")

type Charger struct {
	name           string
	schedules      []entity.Schedule
	capacityLimit  float64 // Capacity limit in Wh calculated based on the SoC limit
	powerLimit     int
	socLimit       float64
	readyToCharge  bool
	isCharging     bool
	soc            float64 // State of Charge from last status
	capacity       float64 // Remaining capacity in Wh from last status
	maxCapacity    float64 // Maximum capacity in Wh (calculated from target SoC)
	stopTime       time.Time
	rate           int // Charge rate in W calculated based on the remaining capacity and time
	client         Client
	status         *entity.SystemStatus
	log            *slog.Logger
	commands       chan ControlCommand
	manualOverride bool
	stop           chan struct{}
	stopped        chan struct{}
	stopOnce       sync.Once
}

func New(name string, client Client, log *slog.Logger) (*Charger, error) {
	return &Charger{
		name:     name,
		client:   client,
		log:      log.With(sl.Module("battery.charge")),
		commands: make(chan ControlCommand, 16),
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}, nil
}

func (c *Charger) SetCapacityLimit(_ int) {
	//c.capacityLimit = float64(capacityLimit)
}

func (c *Charger) SetLimits(powerLimit, socLimit int) {
	c.powerLimit = powerLimit
	c.socLimit = float64(socLimit)
}

func (c *Charger) AddSchedule(schedule entity.Schedule) {
	c.schedules = append(c.schedules, schedule)
}

func (c *Charger) SubmitCommand(cmd ControlCommand) error {
	select {
	case c.commands <- cmd:
		return nil
	default:
		return ErrCommandQueueFull
	}
}

func (c *Charger) Run() error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	defer close(c.stopped)

	for {
		select {
		case cmd := <-c.commands:
			if err := c.processControlCommand(cmd); err != nil {
				c.log.With(sl.Err(err)).Error("processing remote command")
			}
		case <-ticker.C:
			status, err := c.client.Status()
			if err != nil {
				c.log.With(sl.Err(err)).Error("checking battery status")
				observers.UpdateStatus(c.name, "Disconnected")
				continue
			}
			observers.UpdateStatus(c.name, "Connected")
			c.observeStatus(status)

			if len(c.schedules) == 0 {
				if !c.manualOverride {
					continue
				}
			}

			if c.manualOverride {
				c.readyToCharge = true
			} else {
				c.checkTime()
			}
			if c.readyToCharge {
				c.runCharge()
			} else {
				err = c.stopCharge()
				if err != nil {
					c.log.With(sl.Err(err)).Error("stopping charge")
				}
			}
		case <-c.stop:
			return nil
		}
	}
}

// stopCondition checks if the current state of charge (SoC) has reached or exceeded the specified limit.
func (c *Charger) stopCondition() bool {
	return c.soc >= c.socLimit
}

// isTimeToCharge determines whether the current time falls within the specified charge time window.
func (c *Charger) isTimeToCharge(start, stop string) bool {
	now := time.Now()

	// Calculate the start and stop times for today
	startTime, err := timer.ParseTime(start)
	if err != nil {
		c.log.With(sl.Err(err)).Error("parsing start time")
		return false
	}
	stopTime, err := timer.ParseTime(stop)
	if err != nil {
		c.log.With(sl.Err(err)).Error("parsing stop time")
		return false
	}

	// Handle schedules that span midnight (e.g., 22:00 to 06:00)
	if startTime.After(stopTime) {
		// Schedule spans midnight
		stopTime = stopTime.Add(24 * time.Hour)
		// If current time is before the original stop time (early morning hours),
		// the start time should be yesterday, not today
		originalStopTime := stopTime.Add(-24 * time.Hour)
		if now.Before(originalStopTime) {
			startTime = startTime.Add(-24 * time.Hour)
		}
	}

	c.stopTime = stopTime
	// Use !now.Before() to include the exact start time
	return !now.Before(startTime) && now.Before(stopTime)
}

// checkTime determines whether the current time falls within the specified charge time window.
func (c *Charger) checkTime() {
	for _, schedule := range c.schedules {
		if schedule.Enabled && schedule.Type == "charge" {
			if c.isTimeToCharge(schedule.StartTime, schedule.StopTime) {
				oldRate := c.rate
				c.SetLimits(schedule.PowerLimit, schedule.SocLimit)
				c.calculateRate()
				c.readyToCharge = true
				// If already charging and rate changed, update the ongoing charge
				if c.isCharging && c.rate > 0 && c.rate != oldRate {
					c.log.With(
						slog.Int("old_rate", oldRate),
						slog.Int("new_rate", c.rate),
					).Info("updating ongoing charge with new rate from schedule")
					if err := c.client.StartCharge(c.rate); err != nil {
						c.log.With(sl.Err(err)).Error("updating charge rate")
					}
				}
				return
			}
		}
	}

	c.readyToCharge = false
}

// runCharge manages the charge process of the battery based on its current status and predefined limits.
func (c *Charger) runCharge() {
	if c.status == nil {
		return
	}
	log := c.log.With(
		slog.String("operating_mode", c.status.OperatingMode),
		slog.Float64("remaining capacity", c.status.RemainingCapacityWh),
		slog.Float64("SoC", c.status.RSOC),
		slog.Int("rate", c.rate),
		slog.Float64("consumption", c.status.ConsumptionW),
		slog.Bool("charge", c.status.BatteryCharging),
	)

	if c.isCharging {
		if c.stopCondition() {
			log.Info("battery level reached the limit, stopping charge")
			err := c.stopCharge()
			if err != nil {
				c.log.With(sl.Err(err)).Error("stopping charge")
				return
			}
		}
		return
	}

	if c.rate == 0 && !c.isCharging {
		return
	}

	err := c.client.SwitchOperatingModeToManual(c.status.OperatingMode)
	if err != nil {
		c.log.With(sl.Err(err)).Error("switching operating mode")
		return
	}

	log.Info("starting charge")
	err = c.client.StartCharge(c.rate)
	if err != nil {
		c.log.With(sl.Err(err)).Error("starting charge")
		return
	}
	c.isCharging = true
}

// stopCharge stops the current charge activity if it is ongoing.
// Returns an error if the operation fails at any point.
func (c *Charger) stopCharge() error {
	if c.isCharging {
		err := c.client.StopCharge()
		if err != nil {
			return err
		}

		if c.status != nil {
			err = c.client.SwitchOperatingModeToAuto(c.status.OperatingMode)
			if err != nil {
				return err
			}
		}

		c.isCharging = false
	}
	return nil
}

func (c *Charger) processControlCommand(cmd ControlCommand) error {
	log := c.log.With(
		slog.String("command", string(cmd.Type)),
	)

	switch cmd.Type {
	case CommandStartCharge:
		if cmd.Power > 0 {
			c.rate = cmd.Power
		}
		c.manualOverride = true
		c.readyToCharge = true

		currentMode := ""
		if c.status != nil {
			currentMode = c.status.OperatingMode
		}

		if err := c.client.SwitchOperatingModeToManual(currentMode); err != nil {
			return fmt.Errorf("switching to manual mode: %w", err)
		}

		if c.rate <= 0 {
			if c.powerLimit > 0 {
				c.rate = c.powerLimit
			} else {
				return fmt.Errorf("no charge rate configured")
			}
		}

		log.With(slog.Int("rate", c.rate)).Info("starting charge via remote command")
		if err := c.client.StartCharge(c.rate); err != nil {
			return fmt.Errorf("starting charge: %w", err)
		}

		c.isCharging = true
		return nil

	case CommandStopCharge:
		c.manualOverride = false
		log.Info("stopping charge via remote command")
		return c.stopCharge()

	case CommandSetLimits:
		if cmd.Limits == nil {
			return fmt.Errorf("missing limits payload")
		}
		if cmd.Limits.PowerLimit != nil {
			c.powerLimit = *cmd.Limits.PowerLimit
			log = log.With(slog.Int("power_limit", c.powerLimit))
		}
		if cmd.Limits.SocLimit != nil {
			c.socLimit = float64(*cmd.Limits.SocLimit)
			log = log.With(slog.Int("soc_limit", *cmd.Limits.SocLimit))
		}
		c.calculateRate()
		log.With(slog.Int("rate", c.rate)).Info("updated charge limits via remote command")

		if c.manualOverride && c.rate > 0 {
			if err := c.client.StartCharge(c.rate); err != nil {
				return fmt.Errorf("applying updated rate: %w", err)
			}
			c.isCharging = true
		}
		return nil

	case CommandForceMode:
		currentMode := ""
		if c.status != nil {
			currentMode = c.status.OperatingMode
		}
		switch cmd.Mode {
		case OperatingModeManual:
			c.manualOverride = true
			if err := c.client.SwitchOperatingModeToManual(currentMode); err != nil {
				return fmt.Errorf("forcing manual mode: %w", err)
			}
			log.Info("forced manual mode via remote command")
			return nil
		case OperatingModeAuto:
			c.manualOverride = false
			if err := c.client.SwitchOperatingModeToAuto(currentMode); err != nil {
				return fmt.Errorf("forcing auto mode: %w", err)
			}
			log.Info("forced auto mode via remote command")
			return nil
		default:
			return fmt.Errorf("unknown operating mode: %s", cmd.Mode)
		}
	case CommandUpdateConfig:
		if cmd.Config == nil {
			return fmt.Errorf("missing config payload")
		}
		c.schedules = cloneSchedules(cmd.Config.Schedules)
		c.manualOverride = false

		if cmd.Config.PowerLimit != nil {
			c.powerLimit = *cmd.Config.PowerLimit
			log = log.With(slog.Int("power_limit", c.powerLimit))
		}
		if cmd.Config.SocLimit != nil {
			c.socLimit = float64(*cmd.Config.SocLimit)
			log = log.With(slog.Int("soc_limit", *cmd.Config.SocLimit))
		}

		// Check if we should be charging based on updated schedules
		oldRate := c.rate
		c.checkTime()

		// If already charging and rate changed, update the ongoing charge
		if c.isCharging && c.readyToCharge && c.rate > 0 && c.rate != oldRate {
			log.With(
				slog.Int("old_rate", oldRate),
				slog.Int("new_rate", c.rate),
			).Info("updating ongoing charge with new rate from config update")
			if err := c.client.StartCharge(c.rate); err != nil {
				return fmt.Errorf("updating charge rate: %w", err)
			}
		}

		log.Info("applied runtime config update")
		return nil
	default:
		return fmt.Errorf("unsupported command type: %s", cmd.Type)
	}
}

// Stop gracefully terminates the charge worker loop.
func (c *Charger) Stop() {
	c.stopOnce.Do(func() {
		close(c.stop)
	})
	<-c.stopped
}

func cloneSchedules(in []entity.Schedule) []entity.Schedule {
	if len(in) == 0 {
		return nil
	}
	out := make([]entity.Schedule, len(in))
	copy(out, in)
	return out
}

// calculateRate calculates the charge rate in W needed to reach the target SoC within the time window.
func (c *Charger) calculateRate() {
	c.rate = 0
	if c.status == nil || c.status.RSOC <= 0 {
		return
	}

	// Calculate target capacity based on target SoC
	c.maxCapacity = c.socLimit * c.status.RemainingCapacityWh / c.status.RSOC
	if c.maxCapacity <= c.capacity {
		return
	}

	// Calculate how much capacity we need to add
	capacityNeeded := c.maxCapacity - c.capacity
	if capacityNeeded <= 0 {
		return
	}

	remainingTime := time.Until(c.stopTime)
	if remainingTime <= 0 {
		return
	}

	// Calculate rate in W (Wh/h = W) needed to reach target SoC by stop time
	calculatedRate := capacityNeeded / remainingTime.Hours()

	// If power limit is set, use it as the actual charge rate (not just a maximum)
	// This ensures we charge at the specified power limit rather than a lower calculated rate
	if c.powerLimit > 0 {
		// Use power limit as the rate - this allows charging at full specified power
		// The calculated rate is only used to verify we don't exceed hardware limits
		c.rate = c.powerLimit
	} else {
		// No power limit set, use calculated rate
		c.rate = int(calculatedRate)
	}
}

// observeStatus updates various battery status metrics through external observers.
// If the status is nil, the method returns immediately.
func (c *Charger) observeStatus(status *entity.SystemStatus) {
	if status == nil {
		return
	}

	c.status = status
	c.soc = status.USOC
	c.capacity = status.RemainingCapacityWh
	c.maxCapacity = 0
	if status.RSOC > 0 {
		c.maxCapacity = c.socLimit * status.RemainingCapacityWh / status.RSOC
	}

	go func(status *entity.SystemStatus) {
		observers.UpdateSoC(c.name, status.RSOC)
		observers.UpdateUSoC(c.name, status.USOC)
		observers.UpdateCapacity(c.name, status.RemainingCapacityWh)
		observers.UpdateConsumption(c.name, status.ConsumptionW)
		observers.UpdatePac(c.name, status.PacTotalW)
		observers.UpdateChargeState(c.name, status.BatteryCharging)
		observers.UpdateOpMode(c.name, status.OperatingMode)
	}(status)
}
