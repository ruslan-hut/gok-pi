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
	CommandResetGoal    CommandType = "reset_goal"
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

var ErrCommandQueueFull = errors.New("charger command queue full")

type Charger struct {
	name              string
	schedules         []entity.Schedule
	capacityLimit     float64 // Capacity limit in Wh calculated based on the SoC limit
	powerLimit        int     // Current power limit (from schedule if active, otherwise from battery config)
	socLimit          float64 // Current SoC limit (from schedule if active, otherwise from battery config)
	batteryPowerLimit int     // Default power limit from battery config
	batterySocLimit   float64 // Default SoC limit from battery config
	readyToCharge     bool
	isCharging        bool
	soc               float64 // State of Charge from last status
	capacity          float64 // Remaining capacity in Wh from last status
	maxCapacity       float64 // Maximum capacity in Wh (calculated from target SoC)
	stopTime          time.Time
	rate              int // Charge rate in W calculated based on the remaining capacity and time
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

func New(name string, client Client, log *slog.Logger) (*Charger, error) {
	return &Charger{
		name:            name,
		client:          client,
		log:             log.With(sl.Module("battery.charge")),
		commands:        make(chan ControlCommand, 16),
		timezone:        time.UTC, // Default to UTC
		stop:            make(chan struct{}),
		stopped:         make(chan struct{}),
		firstStatusPoll: true,
	}, nil
}

// SetGoalCallbacks sets the callbacks for persisting and clearing goal reached state.
// Goal state is now stored directly in Schedule.GoalReachedTime.
func (c *Charger) SetGoalCallbacks(updateFn func(string, time.Time) error, clearFn func(string) error) {
	c.updateGoalReached = updateFn
	c.clearGoalReached = clearFn
}

func (c *Charger) SetTimezone(timezone string) error {
	loc, err := timer.LoadLocation(timezone)
	if err != nil {
		return fmt.Errorf("invalid timezone %q: %w", timezone, err)
	}
	c.timezone = loc
	return nil
}

func (c *Charger) SetCapacityLimit(_ int) {
	//c.capacityLimit = float64(capacityLimit)
}

func (c *Charger) SetLimits(powerLimit, socLimit int) {
	c.powerLimit = powerLimit
	c.socLimit = float64(socLimit)
	// Store as battery defaults if not already set
	if c.batteryPowerLimit == 0 && c.batterySocLimit == 0 {
		c.batteryPowerLimit = powerLimit
		c.batterySocLimit = float64(socLimit)
	}
}

// SetBatteryDefaults sets the default limits from battery config (used when no schedule is active)
func (c *Charger) SetBatteryDefaults(powerLimit, socLimit int) {
	c.batteryPowerLimit = powerLimit
	c.batterySocLimit = float64(socLimit)
	// If no schedule is active, also update current limits
	if !c.readyToCharge {
		c.powerLimit = powerLimit
		c.socLimit = float64(socLimit)
	}
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

			// Sync internal state with battery on first successful status poll
			if c.firstStatusPoll {
				c.syncStateFromBattery(status)
				c.firstStatusPoll = false
			}

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
	now := time.Now().In(c.timezone)

	// Calculate the start and stop times for today
	startTime, err := timer.ParseTimeInLocation(start, c.timezone)
	if err != nil {
		c.log.With(sl.Err(err)).Error("parsing start time")
		return false
	}
	stopTime, err := timer.ParseTimeInLocation(stop, c.timezone)
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
	now := time.Now().In(c.timezone)

	for i := range c.schedules {
		schedule := &c.schedules[i]
		if schedule.Enabled && schedule.Type == "charge" {
			if c.isTimeToCharge(schedule.StartTime, schedule.StopTime) {
				// Check run_once logic: if enabled and goal was reached, check if stop_time has passed
				if schedule.RunOnce && schedule.GoalReachedTime != nil {
					goalTime := *schedule.GoalReachedTime
					// Parse stop time to check if it has passed
					stopTime, err := timer.ParseTimeInLocation(schedule.StopTime, c.timezone)
					if err == nil {
						// Handle schedules that span midnight
						goalDay := goalTime.In(c.timezone)
						stopTimeOnGoalDay := time.Date(goalDay.Year(), goalDay.Month(), goalDay.Day(),
							stopTime.Hour(), stopTime.Minute(), stopTime.Second(), 0, c.timezone)

						// If stop time is before start time, it spans midnight
						startTime, _ := timer.ParseTimeInLocation(schedule.StartTime, c.timezone)
						if startTime.After(stopTime) {
							stopTimeOnGoalDay = stopTimeOnGoalDay.Add(24 * time.Hour)
						}

						// If stop_time has passed since goal was reached, clear the goal state
						if now.After(stopTimeOnGoalDay) {
							schedule.GoalReachedTime = nil
							if c.clearGoalReached != nil {
								if err := c.clearGoalReached(schedule.Name); err != nil {
									c.log.With(sl.Err(err)).Warn("failed to clear goal reached state")
								}
							}
							c.log.With(
								slog.String("schedule", schedule.Name),
								slog.Time("goal_reached_at", goalTime),
							).Info("stop_time passed, cleared goal reached state for run_once schedule")
						} else {
							// Goal was reached and stop_time hasn't passed yet, skip this schedule
							c.log.With(
								slog.String("schedule", schedule.Name),
								slog.Time("goal_reached_at", goalTime),
							).Debug("run_once schedule already completed, skipping until stop_time passes")
							continue
						}
					}
				}

				oldRate := c.rate
				// Schedule is active: use schedule limits (they take precedence over battery limits)
				c.powerLimit = schedule.PowerLimit
				c.socLimit = float64(schedule.SocLimit)
				c.calculateRate()

				// Check conditions before setting readyToCharge:
				// 1. SOC must be below the limit (we have capacity to charge)
				// 2. Power limit must be valid (> 0)
				// 3. Rate must be valid (> 0)
				canStart := true
				if c.status == nil {
					canStart = false
					c.log.Debug("cannot start charge: no battery status available")
				} else if c.soc >= c.socLimit {
					canStart = false
					c.log.With(
						slog.Float64("usoc", c.soc),
						slog.Float64("soc_limit", c.socLimit),
					).Info("schedule is active but battery already at or above SoC limit, not ready to charge")
				} else if c.powerLimit <= 0 {
					canStart = false
					c.log.With(
						slog.Int("power_limit", c.powerLimit),
					).Info("schedule is active but power limit is invalid, not ready to charge")
				} else if c.rate <= 0 {
					canStart = false
					c.log.With(
						slog.Int("rate", c.rate),
					).Info("schedule is active but calculated rate is invalid, not ready to charge")
				}

				c.readyToCharge = canStart

				// If conditions don't match and battery is in manual mode charging, stop and return to auto mode
				// OperatingMode "1" = manual, "2" = auto
				if !canStart && c.status != nil && c.status.OperatingMode == "1" && (c.isCharging || c.status.BatteryCharging) {
					c.log.With(
						slog.String("operating_mode", c.status.OperatingMode),
						slog.Bool("is_charging", c.isCharging),
						slog.Bool("battery_charging", c.status.BatteryCharging),
					).Info("schedule conditions not met, stopping charge and returning to auto mode")
					if err := c.stopCharge(); err != nil {
						c.log.With(sl.Err(err)).Error("stopping charge and returning to auto mode")
					}
				} else if !canStart && c.status != nil {
					// Log why we're not stopping (for debugging)
					c.log.With(
						slog.String("operating_mode", c.status.OperatingMode),
						slog.Bool("is_charging", c.isCharging),
						slog.Bool("battery_charging", c.status.BatteryCharging),
					).Debug("schedule conditions not met but not stopping charge (checking why)")
				}

				// If already charging and rate changed, update the ongoing charge
				if c.isCharging && c.readyToCharge && c.rate > 0 && c.rate != oldRate {
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

	// No schedule is active: restore battery default limits
	c.readyToCharge = false
	if c.batteryPowerLimit > 0 || c.batterySocLimit > 0 {
		c.powerLimit = c.batteryPowerLimit
		c.socLimit = c.batterySocLimit
	}
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
			// Find the active schedule to check if run_once is enabled
			var activeSchedule *entity.Schedule
			for i := range c.schedules {
				s := &c.schedules[i]
				if s.Enabled && s.Type == "charge" {
					if c.isTimeToCharge(s.StartTime, s.StopTime) {
						activeSchedule = s
						break
					}
				}
			}

			err := c.stopCharge()
			if err != nil {
				c.log.With(sl.Err(err)).Error("stopping charge")
				return
			}

			// If run_once is enabled for the active schedule, record goal reached
			if activeSchedule != nil && activeSchedule.RunOnce {
				goalTime := time.Now()
				activeSchedule.GoalReachedTime = &goalTime
				if c.updateGoalReached != nil {
					if err := c.updateGoalReached(activeSchedule.Name, goalTime); err != nil {
						c.log.With(sl.Err(err)).Warn("failed to persist goal reached state")
					} else {
						c.log.With(
							slog.String("schedule", activeSchedule.Name),
							slog.Time("goal_reached_at", goalTime),
						).Info("recorded goal reached for run_once schedule")
					}
				}
			}
		}
		return
	}

	if c.rate == 0 && !c.isCharging {
		return
	}

	// Don't start charging if USOC is already at or above the SoC limit
	// Charge continues while USOC < SoC limit, stops when USOC >= SoC limit
	if c.soc >= c.socLimit {
		log.With(
			slog.Float64("usoc", c.soc),
			slog.Float64("soc_limit", c.socLimit),
		).Info("battery already at or above SoC limit, not starting charge")
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
	// Check both internal state and actual battery status to handle cases where
	// internal state is out of sync with actual battery state
	shouldStop := c.isCharging || (c.status != nil && c.status.BatteryCharging)

	if shouldStop {
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

		// Build map of old schedules to preserve goal state
		oldScheduleMap := make(map[string]entity.Schedule)
		for _, s := range c.schedules {
			oldScheduleMap[s.Name] = s
		}

		c.schedules = cloneSchedules(cmd.Config.Schedules)

		// Log if manual override is being cleared
		if c.manualOverride {
			log.Info("config update received, clearing manual override mode")
		}
		c.manualOverride = false

		// Preserve GoalReachedTime for existing schedules, clear if run_once was disabled
		for i := range c.schedules {
			if oldSchedule, exists := oldScheduleMap[c.schedules[i].Name]; exists {
				if oldSchedule.GoalReachedTime != nil {
					if c.schedules[i].RunOnce {
						// Preserve goal state if run_once is still enabled
						c.schedules[i].GoalReachedTime = oldSchedule.GoalReachedTime
					} else if c.clearGoalReached != nil {
						// run_once was disabled, clear persisted goal state
						if err := c.clearGoalReached(c.schedules[i].Name); err != nil {
							log.With(sl.Err(err)).Warn("failed to clear goal reached state")
						}
					}
				}
			}
		}

		// Update timezone if provided
		if cmd.Config.Timezone != nil {
			if err := c.SetTimezone(*cmd.Config.Timezone); err != nil {
				log.With(sl.Err(err)).Warn("failed to update timezone")
			} else {
				log.With(slog.String("timezone", *cmd.Config.Timezone)).Info("updated timezone")
			}
		}

		// Update battery default limits if provided
		if cmd.Config.PowerLimit != nil {
			c.batteryPowerLimit = *cmd.Config.PowerLimit
			log = log.With(slog.Int("battery_power_limit", c.batteryPowerLimit))
		}
		if cmd.Config.SocLimit != nil {
			c.batterySocLimit = float64(*cmd.Config.SocLimit)
			log = log.With(slog.Int("battery_soc_limit", *cmd.Config.SocLimit))
		}

		// Check if we should be charging based on updated schedules
		// This will apply schedule limits if active, or battery defaults if not
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

	case CommandResetGoal:
		if cmd.ScheduleName == "" {
			return fmt.Errorf("missing schedule name for reset_goal command")
		}

		// Find and clear goal state for the schedule
		found := false
		for i := range c.schedules {
			if c.schedules[i].Name == cmd.ScheduleName {
				found = true
				if c.schedules[i].GoalReachedTime != nil {
					c.schedules[i].GoalReachedTime = nil
					if c.clearGoalReached != nil {
						if err := c.clearGoalReached(cmd.ScheduleName); err != nil {
							return fmt.Errorf("clearing goal state: %w", err)
						}
					}
					log.With(slog.String("schedule", cmd.ScheduleName)).Info("goal state cleared via remote command")
				} else {
					log.With(slog.String("schedule", cmd.ScheduleName)).Info("no goal state to clear for schedule")
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("schedule %q not found", cmd.ScheduleName)
		}

		// Re-evaluate schedule to potentially start it immediately
		c.checkTime()
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

// calculateRate sets the charge rate to the power limit from the schedule.
func (c *Charger) calculateRate() {
	// Always use the power limit from the schedule as the rate
	c.rate = c.powerLimit
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

// syncStateFromBattery synchronizes internal state with actual battery state on startup.
// This handles cases where the battery is already in a charge state when the agent starts.
func (c *Charger) syncStateFromBattery(status *entity.SystemStatus) {
	if status == nil {
		return
	}

	// If battery is in manual mode and charging, sync our internal state
	// OperatingMode "1" = manual, "2" = auto
	if status.OperatingMode == "1" && status.BatteryCharging {
		if !c.isCharging {
			c.log.With(
				slog.String("operating_mode", status.OperatingMode),
				slog.Bool("battery_charging", status.BatteryCharging),
			).Info("detected battery already charging in manual mode on startup, syncing internal state")
			c.isCharging = true
		}
	}

	// If battery is in auto mode but we think we're charging (stale state), clear it
	if status.OperatingMode == "2" && c.isCharging && !c.manualOverride {
		c.log.Info("battery in auto mode but internal state shows charging, clearing stale state")
		c.isCharging = false
	}
}
