package discharger

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
	CommandUpdateConfig   CommandType = "update_config"
	CommandResetGoal      CommandType = "reset_goal"
)

type OperatingMode string

const (
	OperatingModeManual OperatingMode = "manual"
	OperatingModeAuto   OperatingMode = "auto"
)

type ControlCommand struct {
	Type         CommandType
	Power        int
	Limits       *CommandLimits
	Mode         OperatingMode
	Config       *ConfigUpdate
	ScheduleName string // Used for CommandResetGoal
}

type CommandLimits struct {
	PowerLimit *int
	SocLimit   *int
}

type ConfigUpdate struct {
	Schedules  []entity.Schedule
	PowerLimit *int
	SocLimit   *int
	Timezone   *string
}

var ErrCommandQueueFull = errors.New("discharger command queue full")

type Discharge struct {
	name              string
	schedules         []entity.Schedule
	capacityLimit     float64 // Capacity limit in Wh calculated based on the SoC limit
	powerLimit        int     // Current power limit (from schedule if active, otherwise from battery config)
	socLimit          float64 // Current SoC limit (from schedule if active, otherwise from battery config)
	batteryPowerLimit int     // Default power limit from battery config
	batterySocLimit   float64 // Default SoC limit from battery config
	readyToDischarge  bool
	isDischarging     bool
	soc               float64 // State of Charge from last status
	capacity          float64 // Remaining capacity in Wh from last status
	stopTime          time.Time
	rate              int // Discharge rate in Wh/h calculated based on the remaining capacity and time
	client            Client
	status            *entity.SystemStatus
	log               *slog.Logger
	commands          chan ControlCommand
	manualOverride    bool
	timezone          *time.Location                // Timezone for schedule time parsing
	updateGoalReached func(string, time.Time) error // Callback to persist goal reached state
	clearGoalReached  func(string) error            // Callback to clear goal reached state
	stop              chan struct{}
	stopped           chan struct{}
	stopOnce          sync.Once
	firstStatusPoll   bool // True until first successful status poll
}

func New(name string, client Client, log *slog.Logger) (*Discharge, error) {
	return &Discharge{
		name:            name,
		client:          client,
		log:             log.With(sl.Module("battery.discharge")),
		commands:        make(chan ControlCommand, 16),
		timezone:        time.UTC, // Default to UTC
		stop:            make(chan struct{}),
		stopped:         make(chan struct{}),
		firstStatusPoll: true,
	}, nil
}

// SetGoalCallbacks sets the callbacks for persisting and clearing goal reached state.
// Goal state is now stored directly in Schedule.GoalReachedTime.
func (d *Discharge) SetGoalCallbacks(updateFn func(string, time.Time) error, clearFn func(string) error) {
	d.updateGoalReached = updateFn
	d.clearGoalReached = clearFn
}

func (d *Discharge) SetTimezone(timezone string) error {
	loc, err := timer.LoadLocation(timezone)
	if err != nil {
		return fmt.Errorf("invalid timezone %q: %w", timezone, err)
	}
	d.timezone = loc
	return nil
}

func (d *Discharge) SetCapacityLimit(_ int) {
	//d.capacityLimit = float64(capacityLimit)
}

func (d *Discharge) SetLimits(powerLimit, socLimit int) {
	d.powerLimit = powerLimit
	d.socLimit = float64(socLimit)
	// Store as battery defaults if not already set
	if d.batteryPowerLimit == 0 && d.batterySocLimit == 0 {
		d.batteryPowerLimit = powerLimit
		d.batterySocLimit = float64(socLimit)
	}
}

// SetBatteryDefaults sets the default limits from battery config (used when no schedule is active)
func (d *Discharge) SetBatteryDefaults(powerLimit, socLimit int) {
	d.batteryPowerLimit = powerLimit
	d.batterySocLimit = float64(socLimit)
	// If no schedule is active, also update current limits
	if !d.readyToDischarge {
		d.powerLimit = powerLimit
		d.socLimit = float64(socLimit)
	}
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
	defer close(d.stopped)

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
				observers.UpdateStatus(d.name, "Disconnected")
				continue
			}
			observers.UpdateStatus(d.name, "Connected")
			d.observeStatus(status)

			// Sync internal state with battery on first successful status poll
			if d.firstStatusPoll {
				d.syncStateFromBattery(status)
				d.firstStatusPoll = false
			}

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
		case <-d.stop:
			return nil
		}
	}
}

// stopCondition checks if the current state of charge (SoC) is below the specified limit.
func (d *Discharge) stopCondition() bool {
	return d.socLimit >= d.soc
}

// isTimeToDischarge determines whether the current time falls within the specified discharge time window.
func (d *Discharge) isTimeToDischarge(start, stop string) bool {
	now := time.Now().In(d.timezone)

	// Calculate the start and stop times for today
	startTime, err := timer.ParseTimeInLocation(start, d.timezone)
	if err != nil {
		d.log.With(sl.Err(err)).Error("parsing start time")
		return false
	}
	stopTime, err := timer.ParseTimeInLocation(stop, d.timezone)
	if err != nil {
		d.log.With(sl.Err(err)).Error("parsing stop time")
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

	d.stopTime = stopTime
	// Use !now.Before() to include the exact start time
	return !now.Before(startTime) && now.Before(stopTime)
}

// checkTime determines whether the current time falls within the specified discharge time window.
func (d *Discharge) checkTime() {
	now := time.Now().In(d.timezone)

	for i := range d.schedules {
		schedule := &d.schedules[i]
		if schedule.Enabled && (schedule.Type == "" || schedule.Type == "discharge") {
			if d.isTimeToDischarge(schedule.StartTime, schedule.StopTime) {
				// Check run_once logic: if enabled and goal was reached, check if stop_time has passed
				if schedule.RunOnce && schedule.GoalReachedTime != nil {
					goalTime := *schedule.GoalReachedTime
					// Parse stop time to check if it has passed
					stopTime, err := timer.ParseTimeInLocation(schedule.StopTime, d.timezone)
					if err == nil {
						// Handle schedules that span midnight
						goalDay := goalTime.In(d.timezone)
						stopTimeOnGoalDay := time.Date(goalDay.Year(), goalDay.Month(), goalDay.Day(),
							stopTime.Hour(), stopTime.Minute(), stopTime.Second(), 0, d.timezone)

						// If stop time is before start time, it spans midnight
						startTime, _ := timer.ParseTimeInLocation(schedule.StartTime, d.timezone)
						if startTime.After(stopTime) {
							stopTimeOnGoalDay = stopTimeOnGoalDay.Add(24 * time.Hour)
						}

						// If stop_time has passed since goal was reached, clear the goal state
						if now.After(stopTimeOnGoalDay) {
							schedule.GoalReachedTime = nil
							if d.clearGoalReached != nil {
								if err := d.clearGoalReached(schedule.Name); err != nil {
									d.log.With(sl.Err(err)).Warn("failed to clear goal reached state")
								}
							}
						} else {
							// Goal was reached and stop_time hasn't passed yet, skip this schedule
							d.log.With(
								slog.String("schedule", schedule.Name),
								slog.Time("goal_reached_at", goalTime),
							).Info("schedule has run_once enabled and goal was already reached, skipping until stop_time passes")
							continue
						}
					}
				}

				oldRate := d.rate
				// Schedule is active: use schedule limits (they take precedence over battery limits)
				d.powerLimit = schedule.PowerLimit
				d.socLimit = float64(schedule.SocLimit)
				d.calculateRate()

				// Check conditions before setting readyToDischarge:
				// 1. SOC must be above the limit (we have capacity to discharge)
				// 2. Power limit must be valid (> 0)
				// 3. Rate must be valid (> 0)
				canStart := true
				if d.status == nil {
					canStart = false
					d.log.Debug("cannot start discharge: no battery status available")
				} else if d.soc <= d.socLimit {
					canStart = false
					d.log.With(
						slog.Float64("usoc", d.soc),
						slog.Float64("soc_limit", d.socLimit),
					).Info("schedule is active but battery already at or below SoC limit, not ready to discharge")
				} else if d.powerLimit <= 0 {
					canStart = false
					d.log.With(
						slog.Int("power_limit", d.powerLimit),
					).Info("schedule is active but power limit is invalid, not ready to discharge")
				} else if d.rate <= 0 {
					canStart = false
					d.log.With(
						slog.Int("rate", d.rate),
					).Info("schedule is active but calculated rate is invalid, not ready to discharge")
				}

				d.readyToDischarge = canStart

				// If conditions don't match and battery is in manual mode discharging, stop and return to auto mode
				// OperatingMode "1" = manual, "2" = auto
				if !canStart && d.status != nil && d.status.OperatingMode == "1" && (d.isDischarging || d.status.BatteryDischarging) {
					d.log.With(
						slog.String("operating_mode", d.status.OperatingMode),
						slog.Bool("is_discharging", d.isDischarging),
						slog.Bool("battery_discharging", d.status.BatteryDischarging),
					).Info("schedule conditions not met, stopping discharge and returning to auto mode")
					if err := d.stopDischarge(); err != nil {
						d.log.With(sl.Err(err)).Error("stopping discharge and returning to auto mode")
					}
				} else if !canStart && d.status != nil {
					// Log why we're not stopping (for debugging)
					d.log.With(
						slog.String("operating_mode", d.status.OperatingMode),
						slog.Bool("is_discharging", d.isDischarging),
						slog.Bool("battery_discharging", d.status.BatteryDischarging),
					).Debug("schedule conditions not met but not stopping discharge (checking why)")
				}

				// If already discharging and rate changed, update the ongoing discharge
				if d.isDischarging && d.readyToDischarge && d.rate > 0 && d.rate != oldRate {
					d.log.With(
						slog.Int("old_rate", oldRate),
						slog.Int("new_rate", d.rate),
					).Info("updating ongoing discharge with new rate from schedule")
					if err := d.client.StartDischarge(d.rate); err != nil {
						d.log.With(sl.Err(err)).Error("updating discharge rate")
					}
				}
				return
			}
		}
	}

	// No schedule is active: restore battery default limits
	d.readyToDischarge = false
	if d.batteryPowerLimit > 0 || d.batterySocLimit > 0 {
		d.powerLimit = d.batteryPowerLimit
		d.socLimit = d.batterySocLimit
	}
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
			// Find the active schedule to check if run_once is enabled
			var activeSchedule *entity.Schedule
			for i := range d.schedules {
				s := &d.schedules[i]
				if s.Enabled && (s.Type == "" || s.Type == "discharge") {
					if d.isTimeToDischarge(s.StartTime, s.StopTime) {
						activeSchedule = s
						break
					}
				}
			}

			err := d.stopDischarge()
			if err != nil {
				d.log.With(sl.Err(err)).Error("stopping discharge")
				return
			}

			// If run_once is enabled for the active schedule, record goal reached
			if activeSchedule != nil && activeSchedule.RunOnce {
				goalTime := time.Now()
				activeSchedule.GoalReachedTime = &goalTime
				if d.updateGoalReached != nil {
					if err := d.updateGoalReached(activeSchedule.Name, goalTime); err != nil {
						d.log.With(sl.Err(err)).Warn("failed to persist goal reached state")
					} else {
						d.log.With(
							slog.String("schedule", activeSchedule.Name),
							slog.Time("goal_reached_at", goalTime),
						).Info("recorded goal reached for run_once schedule")
					}
				}
			}
		}
		return
	}

	if d.rate == 0 && !d.isDischarging {
		return
	}

	// Don't start discharging if USOC is already at or below the SoC limit
	// Discharge continues while USOC > SoC limit, stops when USOC <= SoC limit
	if d.soc <= d.socLimit {
		log.With(
			slog.Float64("usoc", d.soc),
			slog.Float64("soc_limit", d.socLimit),
		).Info("battery already at or below SoC limit, not starting discharge")
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
	// Check both internal state and actual battery status to handle cases where
	// internal state is out of sync with actual battery state
	shouldStop := d.isDischarging || (d.status != nil && d.status.BatteryDischarging)

	if shouldStop {
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
	case CommandUpdateConfig:
		if cmd.Config == nil {
			return fmt.Errorf("missing config payload")
		}

		// Build map of old schedules to preserve goal state
		oldScheduleMap := make(map[string]entity.Schedule)
		for _, s := range d.schedules {
			oldScheduleMap[s.Name] = s
		}

		d.schedules = cloneSchedules(cmd.Config.Schedules)

		// Log if manual override is being cleared
		if d.manualOverride {
			log.Info("config update received, clearing manual override mode")
		}
		d.manualOverride = false

		// Preserve GoalReachedTime for existing schedules, clear if run_once was disabled
		for i := range d.schedules {
			if oldSchedule, exists := oldScheduleMap[d.schedules[i].Name]; exists {
				if oldSchedule.GoalReachedTime != nil {
					if d.schedules[i].RunOnce {
						// Preserve goal state if run_once is still enabled
						d.schedules[i].GoalReachedTime = oldSchedule.GoalReachedTime
					} else if d.clearGoalReached != nil {
						// run_once was disabled, clear persisted goal state
						if err := d.clearGoalReached(d.schedules[i].Name); err != nil {
							log.With(sl.Err(err)).Warn("failed to clear goal reached state")
						}
					}
				}
			}
		}

		// Update timezone if provided
		if cmd.Config.Timezone != nil {
			if err := d.SetTimezone(*cmd.Config.Timezone); err != nil {
				log.With(sl.Err(err)).Warn("failed to update timezone")
			} else {
				log.With(slog.String("timezone", *cmd.Config.Timezone)).Info("updated timezone")
			}
		}

		// Update battery default limits if provided
		if cmd.Config.PowerLimit != nil {
			d.batteryPowerLimit = *cmd.Config.PowerLimit
			log = log.With(slog.Int("battery_power_limit", d.batteryPowerLimit))
		}
		if cmd.Config.SocLimit != nil {
			d.batterySocLimit = float64(*cmd.Config.SocLimit)
			log = log.With(slog.Int("battery_soc_limit", *cmd.Config.SocLimit))
		}

		// Check if we should be discharging based on updated schedules
		// This will apply schedule limits if active, or battery defaults if not
		oldRate := d.rate
		d.checkTime()

		// If already discharging and rate changed, update the ongoing discharge
		if d.isDischarging && d.readyToDischarge && d.rate > 0 && d.rate != oldRate {
			log.With(
				slog.Int("old_rate", oldRate),
				slog.Int("new_rate", d.rate),
			).Info("updating ongoing discharge with new rate from config update")
			if err := d.client.StartDischarge(d.rate); err != nil {
				return fmt.Errorf("updating discharge rate: %w", err)
			}
		}

		log.Info("applied runtime config update")
		return nil

	case CommandResetGoal:
		if cmd.ScheduleName == "" {
			return fmt.Errorf("missing schedule name for reset_goal command")
		}

		// Find schedule and clear goal state
		found := false
		for i := range d.schedules {
			if d.schedules[i].Name == cmd.ScheduleName {
				found = true
				if d.schedules[i].GoalReachedTime != nil {
					d.schedules[i].GoalReachedTime = nil
					if d.clearGoalReached != nil {
						if err := d.clearGoalReached(cmd.ScheduleName); err != nil {
							return fmt.Errorf("clearing goal state: %w", err)
						}
					}
					log.With(slog.String("schedule", cmd.ScheduleName)).Info("goal state cleared via remote command")
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("schedule %q not found", cmd.ScheduleName)
		}

		// Re-evaluate schedule to potentially start it immediately
		d.checkTime()
		return nil

	default:
		return fmt.Errorf("unsupported command type: %s", cmd.Type)
	}
}

// Stop gracefully terminates the discharge worker loop.
func (d *Discharge) Stop() {
	d.stopOnce.Do(func() {
		close(d.stop)
	})
	<-d.stopped
}

func cloneSchedules(in []entity.Schedule) []entity.Schedule {
	if len(in) == 0 {
		return nil
	}
	out := make([]entity.Schedule, len(in))
	copy(out, in)
	return out
}

// calculateRate sets the discharge rate to the power limit from the schedule.
func (d *Discharge) calculateRate() {
	// Always use the power limit from the schedule as the rate
	d.rate = d.powerLimit
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

// syncStateFromBattery synchronizes internal state with actual battery state on startup.
// This handles cases where the battery is already in a discharge state when the agent starts.
func (d *Discharge) syncStateFromBattery(status *entity.SystemStatus) {
	if status == nil {
		return
	}

	// If battery is in manual mode and discharging, sync our internal state
	// OperatingMode "1" = manual, "2" = auto
	if status.OperatingMode == "1" && status.BatteryDischarging {
		if !d.isDischarging {
			d.log.With(
				slog.String("operating_mode", status.OperatingMode),
				slog.Bool("battery_discharging", status.BatteryDischarging),
			).Info("detected battery already discharging in manual mode on startup, syncing internal state")
			d.isDischarging = true
		}
	}

	// If battery is in auto mode but we think we're discharging (stale state), clear it
	if status.OperatingMode == "2" && d.isDischarging && !d.manualOverride {
		d.log.Info("battery in auto mode but internal state shows discharging, clearing stale state")
		d.isDischarging = false
	}
}
