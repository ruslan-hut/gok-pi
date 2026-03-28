// Package controller implements a unified battery control loop for both
// charge and discharge operations.
//
// It runs as a long-lived goroutine that polls battery status every 10 seconds,
// evaluates time-based schedules, and controls charge/discharge via battery driver API.
//
// Core state machine (per tick):
//  1. Poll battery status from driver API
//  2. Sync internal state on first poll (handles agent restarts mid-operation)
//  3. Check if current time falls within any enabled schedule for this direction
//  4. If schedule active: apply schedule's power/SoC limits, start operation
//  5. If no schedule active: restore battery default limits, stop operation
//  6. If SoC reaches limit: stop operation, mark "goal reached" for run_once schedules
//
// The controller accepts remote commands (start, stop, set_limits, force_mode,
// update_config, reset_goal) via a buffered channel from the WebSocket client.
//
// Key concepts:
//   - manualOverride: set by remote start command, bypasses schedule checks
//   - run_once schedules: operate once per day, then skip until next schedule period
//   - auto-schedules: generated from electricity prices, prefixed "auto-", auto-removed when expired
//   - goal callbacks: persist "goal reached" state to config.yml so it survives restarts
package controller

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

// Client abstracts the shared battery API operations (status and mode switching).
// Direction-specific operations (start/stop) are provided via the Direction struct.
type Client interface {
	Status() (*entity.SystemStatus, error)
	SwitchOperatingModeToManual(currentMode string) error
	SwitchOperatingModeToAuto(currentMode string) error
}

// Direction captures the behavioral differences between charge and discharge.
type Direction struct {
	// Name is used in log messages ("discharge" or "charge").
	Name string
	// Module is the slog module name ("battery.discharge" or "battery.charge").
	Module string
	// ScheduleFilter returns true if a schedule type should be handled by this controller.
	ScheduleFilter func(scheduleType string) bool
	// GoalReached returns true when SoC has reached the target and the operation should stop.
	GoalReached func(soc, socLimit float64) bool
	// IsActive returns true if the battery is currently performing this operation.
	IsActive func(status *entity.SystemStatus) bool
	// StartOp starts the operation at the given power rate.
	StartOp func(power int) error
	// StopOp stops the operation.
	StopOp func() error
	// ObserveState updates the observer with the current operation state.
	ObserveState func(name string, active bool)
}

// FullClient extends Client with direction-specific start/stop operations.
// This is the interface that driver.Driver satisfies.
type FullClient interface {
	Client
	StartDischarge(power int) error
	StopDischarge() error
	StartCharge(power int) error
	StopCharge() error
}

// DischargeDirection returns a Direction configured for discharge control.
func DischargeDirection(client FullClient) Direction {
	return Direction{
		Name:           "discharge",
		Module:         "battery.discharge",
		ScheduleFilter: func(t string) bool { return t == "" || t == "discharge" },
		GoalReached:    func(soc, limit float64) bool { return soc <= limit },
		IsActive:       func(s *entity.SystemStatus) bool { return s.BatteryDischarging },
		StartOp:        client.StartDischarge,
		StopOp:         client.StopDischarge,
		ObserveState:   observers.UpdateDischargeState,
	}
}

// ChargeDirection returns a Direction configured for charge control.
func ChargeDirection(client FullClient) Direction {
	return Direction{
		Name:           "charge",
		Module:         "battery.charge",
		ScheduleFilter: func(t string) bool { return t == "charge" },
		GoalReached:    func(soc, limit float64) bool { return soc >= limit },
		IsActive:       func(s *entity.SystemStatus) bool { return s.BatteryCharging },
		StartOp:        client.StartCharge,
		StopOp:         client.StopCharge,
		ObserveState:   observers.UpdateChargeState,
	}
}

type CommandType string

const (
	CommandStart        CommandType = "start"
	CommandStop         CommandType = "stop"
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

var ErrCommandQueueFull = errors.New("controller command queue full")

type Controller struct {
	dir               Direction
	name              string
	schedules         []entity.Schedule
	capacityLimit     float64 // Capacity limit in Wh calculated based on the SoC limit
	powerLimit        int     // Current power limit (from schedule if active, otherwise from battery config)
	socLimit          float64 // Current SoC limit (from schedule if active, otherwise from battery config)
	batteryPowerLimit int     // Default power limit from battery config
	batterySocLimit   float64 // Default SoC limit from battery config
	ready             bool    // Ready to start operation
	active            bool    // Operation currently running
	soc               float64 // State of Charge from last status
	capacity          float64 // Remaining capacity in Wh from last status
	stopTime          time.Time
	rate              int // Operation rate in W calculated based on the remaining capacity and time
	client            Client
	status            *entity.SystemStatus
	log               *slog.Logger
	commands          chan ControlCommand
	manualOverride    bool
	timezone          *time.Location                // Timezone for schedule time parsing
	updateGoalReached func(string, time.Time) error // Callback to persist goal reached state
	clearGoalReached  func(string) error            // Callback to clear goal reached state
	removeSchedule    func(string) error            // Callback to remove a schedule from config
	stop              chan struct{}
	stopped           chan struct{}
	stopOnce          sync.Once
	firstStatusPoll   bool // True until first successful status poll
}

func New(name string, client Client, dir Direction, log *slog.Logger) (*Controller, error) {
	return &Controller{
		dir:             dir,
		name:            name,
		client:          client,
		log:             log.With(sl.Module(dir.Module)),
		commands:        make(chan ControlCommand, 16),
		timezone:        time.UTC,
		stop:            make(chan struct{}),
		stopped:         make(chan struct{}),
		firstStatusPoll: true,
	}, nil
}

// SetGoalCallbacks sets the callbacks for persisting and clearing goal reached state.
func (c *Controller) SetGoalCallbacks(updateFn func(string, time.Time) error, clearFn func(string) error) {
	c.updateGoalReached = updateFn
	c.clearGoalReached = clearFn
}

// SetRemoveScheduleCallback sets the callback for removing expired auto-schedules from config.
func (c *Controller) SetRemoveScheduleCallback(fn func(string) error) {
	c.removeSchedule = fn
}

func (c *Controller) SetTimezone(timezone string) error {
	loc, err := timer.LoadLocation(timezone)
	if err != nil {
		return fmt.Errorf("invalid timezone %q: %w", timezone, err)
	}
	c.timezone = loc
	return nil
}

func (c *Controller) SetCapacityLimit(_ int) {
	//c.capacityLimit = float64(capacityLimit)
}

func (c *Controller) SetLimits(powerLimit, socLimit int) {
	c.powerLimit = powerLimit
	c.socLimit = float64(socLimit)
	if c.batteryPowerLimit == 0 && c.batterySocLimit == 0 {
		c.batteryPowerLimit = powerLimit
		c.batterySocLimit = float64(socLimit)
	}
}

// SetBatteryDefaults sets the default limits from battery config (used when no schedule is active)
func (c *Controller) SetBatteryDefaults(powerLimit, socLimit int) {
	c.batteryPowerLimit = powerLimit
	c.batterySocLimit = float64(socLimit)
	if !c.ready {
		c.powerLimit = powerLimit
		c.socLimit = float64(socLimit)
	}
}

func (c *Controller) AddSchedule(schedule entity.Schedule) {
	c.schedules = append(c.schedules, schedule)
}

func (c *Controller) SubmitCommand(cmd ControlCommand) error {
	select {
	case c.commands <- cmd:
		return nil
	default:
		return ErrCommandQueueFull
	}
}

// Run starts the main control loop. It polls battery status every 10 seconds
// and processes remote commands. The loop runs until Stop() is called.
func (c *Controller) Run() error {
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
				c.ready = true
			} else {
				c.checkTime()
			}
			if c.ready {
				c.runOperation()
			} else {
				err = c.stopOperation()
				if err != nil {
					c.log.With(sl.Err(err)).Error("stopping " + c.dir.Name)
				}
			}
		case <-c.stop:
			return nil
		}
	}
}

// isTimeToOperate determines whether the current time falls within the specified time window.
func (c *Controller) isTimeToOperate(start, stop string) bool {
	now := time.Now().In(c.timezone)

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
		stopTime = stopTime.Add(24 * time.Hour)
		originalStopTime := stopTime.Add(-24 * time.Hour)
		if now.Before(originalStopTime) {
			startTime = startTime.Add(-24 * time.Hour)
		}
	}

	c.stopTime = stopTime
	return !now.Before(startTime) && now.Before(stopTime)
}

// checkTime determines whether the current time falls within any enabled schedule.
func (c *Controller) checkTime() {
	now := time.Now().In(c.timezone)

	for i := range c.schedules {
		schedule := &c.schedules[i]
		if schedule.Enabled && c.dir.ScheduleFilter(schedule.Type) {
			if c.isTimeToOperate(schedule.StartTime, schedule.StopTime) {
				// Check run_once logic: if enabled and goal was reached, don't run on same day
				// OR until the schedule period ends (whichever is later)
				if schedule.RunOnce && schedule.GoalReachedTime != nil {
					goalTime := *schedule.GoalReachedTime
					goalDay := goalTime.In(c.timezone)

					sameDay := goalDay.Year() == now.Year() && goalDay.YearDay() == now.YearDay()

					stopTime, err := timer.ParseTimeInLocation(schedule.StopTime, c.timezone)
					if err != nil {
						c.log.With(sl.Err(err)).Error("parsing stop time for run_once check")
						continue
					}

					stopTimeOnGoalDay := time.Date(goalDay.Year(), goalDay.Month(), goalDay.Day(),
						stopTime.Hour(), stopTime.Minute(), stopTime.Second(), 0, c.timezone)

					startTime, _ := timer.ParseTimeInLocation(schedule.StartTime, c.timezone)
					if startTime.After(stopTime) {
						stopTimeOnGoalDay = stopTimeOnGoalDay.Add(24 * time.Hour)
					}

					schedulePeriodEnded := now.After(stopTimeOnGoalDay)

					if sameDay || !schedulePeriodEnded {
						c.log.With(
							slog.String("schedule", schedule.Name),
							slog.Time("goal_reached_at", goalTime),
							slog.Bool("same_day", sameDay),
							slog.Bool("period_ended", schedulePeriodEnded),
						).Info("run_once schedule goal reached, skipping until next period")
						continue
					}

					schedule.GoalReachedTime = nil
					if c.clearGoalReached != nil {
						if err := c.clearGoalReached(schedule.Name); err != nil {
							c.log.With(sl.Err(err)).Warn("failed to clear goal reached state")
						}
					}
					c.log.With(
						slog.String("schedule", schedule.Name),
					).Info("cleared goal state for run_once schedule (new period started)")
				}

				oldRate := c.rate
				c.powerLimit = schedule.PowerLimit
				c.socLimit = float64(schedule.SocLimit)
				c.calculateRate()

				canStart := true
				if c.status == nil {
					canStart = false
					c.log.Debug("cannot start " + c.dir.Name + ": no battery status available")
				} else if c.dir.GoalReached(c.soc, c.socLimit) {
					canStart = false
					if schedule.RunOnce {
						goalTime := time.Now()
						schedule.GoalReachedTime = &goalTime
						if c.updateGoalReached != nil {
							if err := c.updateGoalReached(schedule.Name, goalTime); err != nil {
								c.log.With(sl.Err(err)).Warn("failed to persist goal reached state")
							}
						}
						c.log.With(
							slog.String("schedule", schedule.Name),
							slog.Float64("usoc", c.soc),
							slog.Float64("soc_limit", c.socLimit),
						).Info("run_once schedule: battery already at goal, marking as reached")
						continue
					}
					c.log.With(
						slog.Float64("usoc", c.soc),
						slog.Float64("soc_limit", c.socLimit),
					).Debug("schedule is active but battery already at SoC limit, not ready to " + c.dir.Name)
				} else if c.powerLimit <= 0 {
					canStart = false
					c.log.With(
						slog.Int("power_limit", c.powerLimit),
					).Info("schedule is active but power limit is invalid, not ready to " + c.dir.Name)
				} else if c.rate <= 0 {
					canStart = false
					c.log.With(
						slog.Int("rate", c.rate),
					).Info("schedule is active but calculated rate is invalid, not ready to " + c.dir.Name)
				}

				c.ready = canStart

				if !canStart && c.status != nil && c.status.OperatingMode == "1" && (c.active || c.dir.IsActive(c.status)) {
					c.log.With(
						slog.String("operating_mode", c.status.OperatingMode),
						slog.Bool("is_active", c.active),
						slog.Bool("battery_active", c.dir.IsActive(c.status)),
					).Info("schedule conditions not met, stopping " + c.dir.Name + " and returning to auto mode")
					if err := c.stopOperation(); err != nil {
						c.log.With(sl.Err(err)).Error("stopping " + c.dir.Name + " and returning to auto mode")
					}
				} else if !canStart && c.status != nil {
					c.log.With(
						slog.String("operating_mode", c.status.OperatingMode),
						slog.Bool("is_active", c.active),
						slog.Bool("battery_active", c.dir.IsActive(c.status)),
					).Debug("schedule conditions not met but not stopping " + c.dir.Name + " (checking why)")
				}

				if c.active && c.ready && c.rate > 0 && c.rate != oldRate {
					c.log.With(
						slog.Int("old_rate", oldRate),
						slog.Int("new_rate", c.rate),
					).Info("updating ongoing " + c.dir.Name + " with new rate from schedule")
					if err := c.dir.StartOp(c.rate); err != nil {
						c.log.With(sl.Err(err)).Error("updating " + c.dir.Name + " rate")
					}
				}
				return
			}
		}
	}

	// No schedule is active: restore battery default limits
	c.ready = false
	if c.batteryPowerLimit > 0 || c.batterySocLimit > 0 {
		c.powerLimit = c.batteryPowerLimit
		c.socLimit = c.batterySocLimit
	}

	c.removeExpiredAutoSchedules()
}

// removeExpiredAutoSchedules removes auto-schedules whose time window has ended.
func (c *Controller) removeExpiredAutoSchedules() {
	if c.removeSchedule == nil {
		return
	}

	now := time.Now().In(c.timezone)
	var remaining []entity.Schedule

	for _, s := range c.schedules {
		if !entity.IsAutoSchedule(s.Name) {
			remaining = append(remaining, s)
			continue
		}

		startTime, err := timer.ParseTimeInLocation(s.StartTime, c.timezone)
		if err != nil {
			remaining = append(remaining, s)
			continue
		}
		stopTime, err := timer.ParseTimeInLocation(s.StopTime, c.timezone)
		if err != nil {
			remaining = append(remaining, s)
			continue
		}

		expired := false
		if startTime.Before(stopTime) {
			expired = !now.Before(stopTime)
		} else {
			expired = !now.Before(stopTime) && now.Before(startTime)
		}

		if expired {
			c.log.With(slog.String("schedule", s.Name)).Info("removing expired auto-schedule")
			if err := c.removeSchedule(s.Name); err != nil {
				c.log.With(slog.String("schedule", s.Name), sl.Err(err)).Warn("failed to remove expired auto-schedule")
			}
			continue
		}

		remaining = append(remaining, s)
	}

	c.schedules = remaining
}

// runOperation manages the operation based on current status and limits.
func (c *Controller) runOperation() {
	if c.status == nil {
		return
	}
	log := c.log.With(
		slog.String("operating_mode", c.status.OperatingMode),
		slog.Float64("remaining capacity", c.status.RemainingCapacityWh),
		slog.Float64("SoC", c.status.RSOC),
		slog.Int("rate", c.rate),
		slog.Float64("consumption", c.status.ConsumptionW),
		slog.Bool(c.dir.Name, c.dir.IsActive(c.status)),
	)

	if c.active {
		if c.dir.GoalReached(c.soc, c.socLimit) {
			log.Info("battery level reached the limit, stopping " + c.dir.Name)
			var activeSchedule *entity.Schedule
			for i := range c.schedules {
				s := &c.schedules[i]
				if s.Enabled && c.dir.ScheduleFilter(s.Type) {
					if c.isTimeToOperate(s.StartTime, s.StopTime) {
						activeSchedule = s
						break
					}
				}
			}

			err := c.stopOperation()
			if err != nil {
				c.log.With(sl.Err(err)).Error("stopping " + c.dir.Name)
				return
			}

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

	if c.rate == 0 && !c.active {
		return
	}

	if c.dir.GoalReached(c.soc, c.socLimit) {
		log.With(
			slog.Float64("usoc", c.soc),
			slog.Float64("soc_limit", c.socLimit),
		).Info("battery already at SoC limit, not starting " + c.dir.Name)
		return
	}

	err := c.client.SwitchOperatingModeToManual(c.status.OperatingMode)
	if err != nil {
		c.log.With(sl.Err(err)).Error("switching operating mode")
		return
	}

	log.Info("starting " + c.dir.Name)
	err = c.dir.StartOp(c.rate)
	if err != nil {
		c.log.With(sl.Err(err)).Error("starting " + c.dir.Name)
		return
	}
	c.active = true
}

// stopOperation stops the current operation if it is ongoing.
func (c *Controller) stopOperation() error {
	shouldStop := c.active || (c.status != nil && c.dir.IsActive(c.status))

	if shouldStop {
		err := c.dir.StopOp()
		if err != nil {
			return err
		}

		if c.status != nil {
			err = c.client.SwitchOperatingModeToAuto(c.status.OperatingMode)
			if err != nil {
				return err
			}
		}

		c.active = false
	}
	return nil
}

func (c *Controller) processControlCommand(cmd ControlCommand) error {
	log := c.log.With(
		slog.String("command", string(cmd.Type)),
	)

	switch cmd.Type {
	case CommandStart:
		if cmd.Power > 0 {
			c.rate = cmd.Power
		}
		c.manualOverride = true
		c.ready = true

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
				return fmt.Errorf("no %s rate configured", c.dir.Name)
			}
		}

		log.With(slog.Int("rate", c.rate)).Info("starting " + c.dir.Name + " via remote command")
		if err := c.dir.StartOp(c.rate); err != nil {
			return fmt.Errorf("starting %s: %w", c.dir.Name, err)
		}

		c.active = true
		return nil

	case CommandStop:
		c.manualOverride = false
		log.Info("stopping " + c.dir.Name + " via remote command")
		return c.stopOperation()

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
		log.With(slog.Int("rate", c.rate)).Info("updated " + c.dir.Name + " limits via remote command")

		if c.manualOverride && c.rate > 0 {
			if err := c.dir.StartOp(c.rate); err != nil {
				return fmt.Errorf("applying updated rate: %w", err)
			}
			c.active = true
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

		oldScheduleMap := make(map[string]entity.Schedule)
		for _, s := range c.schedules {
			oldScheduleMap[s.Name] = s
		}

		c.schedules = entity.CloneSchedules(cmd.Config.Schedules)

		if c.manualOverride {
			log.Info("config update received, clearing manual override mode")
		}
		c.manualOverride = false

		for i := range c.schedules {
			if oldSchedule, exists := oldScheduleMap[c.schedules[i].Name]; exists {
				if oldSchedule.GoalReachedTime != nil {
					if c.schedules[i].RunOnce {
						c.schedules[i].GoalReachedTime = oldSchedule.GoalReachedTime
					} else if c.clearGoalReached != nil {
						if err := c.clearGoalReached(c.schedules[i].Name); err != nil {
							log.With(sl.Err(err)).Warn("failed to clear goal reached state")
						}
					}
				}
			}
		}

		if cmd.Config.Timezone != nil {
			if err := c.SetTimezone(*cmd.Config.Timezone); err != nil {
				log.With(sl.Err(err)).Warn("failed to update timezone")
			} else {
				log.With(slog.String("timezone", *cmd.Config.Timezone)).Info("updated timezone")
			}
		}

		if cmd.Config.PowerLimit != nil {
			c.batteryPowerLimit = *cmd.Config.PowerLimit
			log = log.With(slog.Int("battery_power_limit", c.batteryPowerLimit))
		}
		if cmd.Config.SocLimit != nil {
			c.batterySocLimit = float64(*cmd.Config.SocLimit)
			log = log.With(slog.Int("battery_soc_limit", *cmd.Config.SocLimit))
		}

		oldRate := c.rate
		c.checkTime()

		if c.active && c.ready && c.rate > 0 && c.rate != oldRate {
			log.With(
				slog.Int("old_rate", oldRate),
				slog.Int("new_rate", c.rate),
			).Info("updating ongoing " + c.dir.Name + " with new rate from config update")
			if err := c.dir.StartOp(c.rate); err != nil {
				return fmt.Errorf("updating %s rate: %w", c.dir.Name, err)
			}
		}

		log.Info("applied runtime config update")
		return nil

	case CommandResetGoal:
		if cmd.ScheduleName == "" {
			return fmt.Errorf("missing schedule name for reset_goal command")
		}

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
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("schedule %q not found", cmd.ScheduleName)
		}

		c.checkTime()
		return nil

	default:
		return fmt.Errorf("unsupported command type: %s", cmd.Type)
	}
}

// Stop gracefully terminates the controller loop.
func (c *Controller) Stop() {
	c.stopOnce.Do(func() {
		close(c.stop)
	})
	<-c.stopped
}

// GetScheduleType returns the type of a schedule by name.
func (c *Controller) GetScheduleType(name string) (string, bool) {
	for _, s := range c.schedules {
		if s.Name == name {
			return s.Type, true
		}
	}
	return "", false
}


// calculateRate sets the operation rate to the power limit from the schedule.
func (c *Controller) calculateRate() {
	c.rate = c.powerLimit
}

// observeStatus updates various battery status metrics through external observers.
func (c *Controller) observeStatus(status *entity.SystemStatus) {
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
		c.dir.ObserveState(c.name, c.dir.IsActive(status))
		observers.UpdateOpMode(c.name, status.OperatingMode)
	}(status)
}

// syncStateFromBattery synchronizes internal state with actual battery state on startup.
func (c *Controller) syncStateFromBattery(status *entity.SystemStatus) {
	if status == nil {
		return
	}

	// If battery is in manual mode and operation is active, sync our internal state
	// OperatingMode "1" = manual, "2" = auto
	if status.OperatingMode == "1" && c.dir.IsActive(status) {
		if !c.active {
			c.log.With(
				slog.String("operating_mode", status.OperatingMode),
				slog.Bool("battery_active", c.dir.IsActive(status)),
			).Info("detected battery already " + c.dir.Name + "ing in manual mode on startup, syncing internal state")
			c.active = true
		}
	}

	// If battery is in auto mode but we think we're active (stale state), clear it
	if status.OperatingMode == "2" && c.active && !c.manualOverride {
		c.log.Info("battery in auto mode but internal state shows " + c.dir.Name + "ing, clearing stale state")
		c.active = false
	}
}
