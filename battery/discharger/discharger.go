package discharger

import (
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
}

func New(name string, client Client, log *slog.Logger) (*Discharge, error) {
	return &Discharge{
		name:   name,
		client: client,
		log:    log.With(sl.Module("battery.discharge")),
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

func (d *Discharge) Run() error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			status, err := d.client.Status()
			if err != nil {
				d.log.With(sl.Err(err)).Error("checking battery status")
				continue
			}
			d.observeStatus(status)

			if len(d.schedules) == 0 {
				continue
			}

			d.checkTime()
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
