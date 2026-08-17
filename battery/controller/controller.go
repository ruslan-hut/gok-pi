// Package controller implements a unified battery control loop for both
// charge and discharge operations.
//
// It runs as a long-lived goroutine that reacts to battery status readings delivered
// by a shared StatusPoller (one poll per battery, fanned out to both directions),
// evaluates time-based schedules, and controls charge/discharge via battery driver API.
//
// Core state machine (per status reading):
//  1. Receive battery status from the shared StatusPoller
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
//   - manualOverride: set by remote start command, bypasses schedule checks. Its
//     overrideSource says who asked: an operator's override is cancelled by the
//     next config push, one driven by an external event (an EV charging session)
//     is not — only the event that started it ends it.
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
	// CanStart returns true when SoC is far enough past the target to (re)start the
	// operation. It is deliberately stricter than !GoalReached: the deadband between
	// the two is what stops the controller flapping when SoC sits exactly on the limit.
	CanStart func(soc, socLimit, deadband float64) bool
	// IsActive returns true if the battery is currently performing this operation.
	IsActive func(status *entity.SystemStatus) bool
	// StartOp starts the operation at the given power rate.
	StartOp func(power int) error
	// StopOp stops the operation.
	StopOp func() error
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
		CanStart:       func(soc, limit, deadband float64) bool { return soc >= limit+deadband },
		IsActive:       func(s *entity.SystemStatus) bool { return s.BatteryDischarging },
		StartOp:        client.StartDischarge,
		StopOp:         client.StopDischarge,
	}
}

// ChargeDirection returns a Direction configured for charge control.
func ChargeDirection(client FullClient) Direction {
	return Direction{
		Name:           "charge",
		Module:         "battery.charge",
		ScheduleFilter: func(t string) bool { return t == "charge" },
		GoalReached:    func(soc, limit float64) bool { return soc >= limit },
		CanStart:       func(soc, limit, deadband float64) bool { return soc <= limit-deadband },
		IsActive:       func(s *entity.SystemStatus) bool { return s.BatteryCharging },
		StartOp:        client.StartCharge,
		StopOp:         client.StopCharge,
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

// Operating modes as the battery reports them in SystemStatus.OperatingMode.
const (
	batteryModeManual = "1"
	batteryModeAuto   = "2"
)

// CommandSourceCharger marks a start command issued because an EV charging
// session began. Such an override outlives config pushes; see the comment on
// Controller.overrideSource.
const CommandSourceCharger = "charger"

type ControlCommand struct {
	Type         CommandType
	Power        int
	Limits       *CommandLimits
	Mode         OperatingMode
	Config       *ConfigUpdate
	ScheduleName string // Used for CommandResetGoal
	Source       string // Who asked: "" for a UI/manual command, CommandSourceCharger for an EV session
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
	deadbandHold      bool    // SoC is inside the restart deadband but has not reached the limit: keep a running operation alive
	socDeadband       float64 // SoC margin past socLimit required to (re)start, see DefaultSocDeadband
	minDwell          time.Duration
	lastTransition    time.Time // When the operation last started or stopped
	soc               float64   // State of Charge from last status
	capacity          float64   // Remaining capacity in Wh from last status
	stopTime          time.Time
	rate              int // Operation rate in W calculated based on the remaining capacity and time
	client            Client
	mode              *ModeCoordinator // shared with the opposite-direction controller for this battery
	status            *entity.SystemStatus
	log               *slog.Logger
	commands          chan ControlCommand
	manualOverride    bool
	// overrideSource records why manualOverride is set. A config push cancels an
	// operator's manual override — that is intentional, the new config supersedes
	// it — but must not cancel one driven by an external event that is still
	// running, such as an EV charging session: the server pushes config whenever
	// auto-schedules change, which would silently drop the car back to grid power
	// mid-charge.
	overrideSource string
	// publishedOverride is the last value pushed to the observers, so the
	// override is reported only when it actually changes rather than on every
	// poll (each snapshot update costs a spool write and an uplink row).
	publishedOverride string
	timezone          *time.Location                // Timezone for schedule time parsing
	updateGoalReached func(string, time.Time) error // Callback to persist goal reached state
	clearGoalReached  func(string) error            // Callback to clear goal reached state
	removeSchedule    func(string) error            // Callback to remove a schedule from config
	stop              chan struct{}
	stopped           chan struct{}
	stopOnce          sync.Once
	firstStatusPoll   bool                        // True until first successful status poll
	statusFeed        <-chan *entity.SystemStatus // Shared status readings from the battery's StatusPoller
}

const (
	// DefaultSocDeadband is how far (in SoC percentage points) past the SoC limit the
	// battery must be before an operation may (re)start. Without it, a battery sitting
	// exactly on its limit alternates between "goal reached" and "not reached" on
	// consecutive 10s readings, and the controller writes an operating-mode change to
	// the battery on every flip.
	DefaultSocDeadband = 2.0

	// DefaultMinDwell is the minimum time after stopping an operation before it may
	// start again. It backstops the deadband for flapping that SoC alone does not
	// explain, e.g. a power limit toggling across zero via config pushes.
	DefaultMinDwell = 5 * time.Minute
)

func New(name string, client Client, dir Direction, log *slog.Logger) (*Controller, error) {
	return &Controller{
		dir:             dir,
		name:            name,
		client:          client,
		log:             log.With(sl.Module(dir.Module)),
		commands:        make(chan ControlCommand, 16),
		timezone:        time.UTC,
		socDeadband:     DefaultSocDeadband,
		minDwell:        DefaultMinDwell,
		stop:            make(chan struct{}),
		stopped:         make(chan struct{}),
		firstStatusPoll: true,
	}, nil
}

// SetHysteresis overrides the anti-flapping parameters. A zero or negative value
// leaves the corresponding default in place.
func (c *Controller) SetHysteresis(socDeadband float64, minDwell time.Duration) {
	if socDeadband > 0 {
		c.socDeadband = socDeadband
	}
	if minDwell > 0 {
		c.minDwell = minDwell
	}
}

// canStartNow reports whether the minimum dwell since the last start/stop has
// elapsed. It gates starting only — stopping is always allowed, since stopping is
// the safe direction and must never be delayed by anti-flapping logic.
func (c *Controller) canStartNow() (bool, time.Duration) {
	if c.lastTransition.IsZero() || c.minDwell <= 0 {
		return true, 0
	}
	if remaining := c.minDwell - time.Since(c.lastTransition); remaining > 0 {
		return false, remaining
	}
	return true, 0
}

// SetModeCoordinator sets the shared operating-mode coordinator. Both the charge and
// discharge controllers for a battery must be given the same coordinator instance.
func (c *Controller) SetModeCoordinator(m *ModeCoordinator) {
	c.mode = m
}

// SetStatusFeed wires the controller to a StatusPoller subscription. The controller no
// longer polls the battery itself; it reacts to readings delivered on this channel, so
// all controllers and the monitor for a battery share a single status request per tick.
func (c *Controller) SetStatusFeed(feed <-chan *entity.SystemStatus) {
	c.statusFeed = feed
}

// switchToManual switches the battery to manual mode through the coordinator (if set),
// so the opposite-direction controller does not switch it back to auto underneath us.
func (c *Controller) switchToManual() error {
	currentMode := ""
	if c.status != nil {
		currentMode = c.status.OperatingMode
	}
	fn := func() error { return c.client.SwitchOperatingModeToManual(currentMode) }
	if c.mode != nil {
		return c.mode.AcquireManual(c.dir.Name, fn)
	}
	return fn()
}

// switchToAuto releases this direction's manual ownership and switches the battery
// back to auto only when no other direction is still operating.
func (c *Controller) switchToAuto() error {
	currentMode := ""
	if c.status != nil {
		currentMode = c.status.OperatingMode
	}
	fn := func() error { return c.client.SwitchOperatingModeToAuto(currentMode) }
	if c.mode != nil {
		return c.mode.ReleaseToAuto(c.dir.Name, fn)
	}
	return fn()
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

// Run starts the main control loop. It reacts to battery status readings delivered by
// the shared StatusPoller (set via SetStatusFeed) and processes remote commands. The
// loop runs until Stop() is called.
func (c *Controller) Run() error {
	defer close(c.stopped)

	for {
		select {
		case cmd := <-c.commands:
			if err := c.processControlCommand(cmd); err != nil {
				c.log.With(sl.Err(err)).Error("processing remote command")
			}
			c.publishOverride()
		case status := <-c.statusFeed:
			if status == nil {
				c.invalidateStatus()
				continue
			}

			c.observeStatus(status)

			if c.firstStatusPoll {
				c.syncStateFromBattery(status)
				c.firstStatusPoll = false
				c.publishOverride()
			}

			if len(c.schedules) == 0 {
				if !c.manualOverride {
					continue
				}
			}

			c.evaluate()
		case <-c.stop:
			return nil
		}
	}
}

// evaluate decides what to do with the current battery reading: re-check the
// schedules, then start, keep running, or stop the operation.
func (c *Controller) evaluate() {
	if c.status == nil {
		// No trustworthy reading. Commanding the battery now would be guesswork,
		// and the stop path would clear c.active while the battery keeps running.
		return
	}

	if c.manualOverride {
		c.ready = true
		c.deadbandHold = false
	} else {
		c.checkTime()
	}

	switch {
	case c.ready:
		c.runOperation()
	case c.deadbandHold && c.active:
		// Not startable, but only because SoC sits inside the restart deadband.
		// Stopping here would put start and stop on the same threshold and flap
		// the battery once per minDwell; runOperation stops the operation once
		// the true SoC limit is reached.
		c.runOperation()
	default:
		if err := c.stopOperation(); err != nil {
			c.log.With(sl.Err(err)).Error("stopping " + c.dir.Name)
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

// hasActiveSchedule reports whether any enabled schedule for this direction is
// currently within its operating window.
func (c *Controller) hasActiveSchedule() bool {
	for i := range c.schedules {
		s := &c.schedules[i]
		if s.Enabled && c.dir.ScheduleFilter(s.Type) && c.isTimeToOperate(s.StartTime, s.StopTime) {
			return true
		}
	}
	return false
}

// checkTime determines whether the current time falls within any enabled schedule.
func (c *Controller) checkTime() {
	now := time.Now().In(c.timezone)
	c.deadbandHold = false

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
				} else if !c.dir.CanStart(c.soc, c.socLimit, c.socDeadband) {
					canStart = false
					// The run_once goal is marked only when the true limit is reached,
					// not merely when SoC is inside the deadband, so the deadband can
					// never cause a schedule to be consumed early.
					if schedule.RunOnce && c.dir.GoalReached(c.soc, c.socLimit) {
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
					// Inside the deadband but short of the limit: not startable, yet
					// not a reason to stop an operation that is already running.
					c.deadbandHold = !c.dir.GoalReached(c.soc, c.socLimit)
					c.log.With(
						slog.Float64("usoc", c.soc),
						slog.Float64("soc_limit", c.socLimit),
						slog.Float64("soc_deadband", c.socDeadband),
					).Debug("schedule is active but battery is at or within the deadband of the SoC limit, not ready to " + c.dir.Name)
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

				if !canStart && !c.deadbandHold && c.status != nil && c.status.OperatingMode == batteryModeManual && (c.active || c.dir.IsActive(c.status)) {
					c.log.With(
						slog.String("operating_mode", c.status.OperatingMode),
						slog.Bool("is_active", c.active),
						slog.Bool("battery_active", c.dir.IsActive(c.status)),
					).Info("schedule conditions not met, stopping " + c.dir.Name + " and returning to auto mode")
					if err := c.stopOperation(); err != nil {
						c.log.With(sl.Err(err)).Error("stopping " + c.dir.Name + " and returning to auto mode")
					}
				} else if !canStart && c.status != nil && c.status.OperatingMode != "1" {
					// The battery is not in manual mode, so there is nothing to stop.
					// Clear any stale active flag: leaving it set would make the
					// controller believe an operation is running that is not, which
					// suppresses later starts and corrupts the rate-update branch below.
					if c.active {
						c.log.With(
							slog.String("operating_mode", c.status.OperatingMode),
						).Info("battery is not in manual mode, clearing stale active " + c.dir.Name + " state")
						c.active = false
						c.lastTransition = time.Now()
					}
					c.log.With(
						slog.String("operating_mode", c.status.OperatingMode),
						slog.Bool("battery_active", c.dir.IsActive(c.status)),
					).Debug("schedule conditions not met but not stopping " + c.dir.Name)
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
// Both direction controllers for a battery hold the full schedule list, so each one
// only prunes the schedules it actually drives: otherwise the charge controller
// deletes a discharge schedule from the shared config on its own clock while the
// discharge controller is still running it.
func (c *Controller) removeExpiredAutoSchedules() {
	if c.removeSchedule == nil {
		return
	}

	now := time.Now().In(c.timezone)
	var remaining []entity.Schedule

	for _, s := range c.schedules {
		if !entity.IsAutoSchedule(s.Name) || !c.dir.ScheduleFilter(s.Type) {
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

	if !c.dir.CanStart(c.soc, c.socLimit, c.socDeadband) {
		log.With(
			slog.Float64("usoc", c.soc),
			slog.Float64("soc_limit", c.socLimit),
			slog.Float64("soc_deadband", c.socDeadband),
		).Info("battery at or within the deadband of the SoC limit, not starting " + c.dir.Name)
		return
	}

	// Anti-flapping backstop: never restart within minDwell of the last transition.
	if ok, remaining := c.canStartNow(); !ok {
		log.With(
			slog.Duration("retry_in", remaining.Truncate(time.Second)),
		).Debug("within minimum dwell since last transition, not starting " + c.dir.Name)
		return
	}

	err := c.switchToManual()
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
	c.lastTransition = time.Now()
}

// publishOverride reports the current override state to the observers so the
// control server and the UI can tell a schedule-driven operation from one an
// operator or an EV charging session forced. It is a no-op when nothing changed.
func (c *Controller) publishOverride() {
	source := ""
	if c.manualOverride {
		source = c.overrideSource
		if source == "" {
			source = string(OperatingModeManual)
		}
	}
	if source == c.publishedOverride {
		return
	}
	c.publishedOverride = source
	observers.UpdateOverride(c.name, c.dir.Name, source)
}

// stopOperation stops the current operation if it is ongoing.
//
// Only an operation running in manual mode is this controller's to stop. In auto
// mode the battery charges and discharges on its own, and matching on the direction
// flag alone made every reading of a battery taking PV into store look like a charge
// that needed stopping: one setpoint POST and one "stopped charge" line per poll for
// as long as the sun was out.
func (c *Controller) stopOperation() error {
	batteryOperating := c.status != nil &&
		c.status.OperatingMode == batteryModeManual &&
		c.dir.IsActive(c.status)
	shouldStop := c.active || batteryOperating

	if shouldStop {
		err := c.dir.StopOp()
		if err != nil {
			return err
		}

		releasedManual := false
		if c.status != nil {
			err = c.switchToAuto()
			if err != nil {
				return err
			}
			releasedManual = true
		}

		wasActive := c.active
		c.active = false
		c.lastTransition = time.Now()

		// Every stop is logged here, including the ordinary end-of-window one that
		// no branch above announces. Without it the log shows a discharge starting
		// and never ending, and whether the battery was handed back cannot be
		// established after the fact.
		//
		// released_manual is this controller's claim on manual mode, not the
		// battery's resulting mode: the coordinator keeps the battery in manual
		// while the opposite direction is still running, so promising "returned to
		// auto" here would sometimes be a lie.
		c.log.With(
			slog.Bool("was_active", wasActive),
			slog.Bool("released_manual", releasedManual),
			slog.Float64("usoc", c.soc),
			slog.Float64("soc_limit", c.socLimit),
		).Info("stopped " + c.dir.Name)
	}
	return nil
}

func (c *Controller) processControlCommand(cmd ControlCommand) error {
	log := c.log.With(
		slog.String("command", string(cmd.Type)),
	)

	switch cmd.Type {
	case CommandStart:
		// Limits arrive with the start when the caller drives the battery from an
		// external event, so the operation is configured and started by a single
		// ordered command instead of a set_limits/start pair that could be split.
		// They are applied before the rate is derived and before the SoC guard, so
		// the caller's floor is the one enforced.
		if cmd.Limits != nil {
			if cmd.Limits.PowerLimit != nil {
				c.powerLimit = *cmd.Limits.PowerLimit
			}
			if cmd.Limits.SocLimit != nil {
				c.socLimit = float64(*cmd.Limits.SocLimit)
			}
			c.calculateRate()
		}

		rate := c.rate
		if cmd.Power > 0 {
			rate = cmd.Power
		}
		if rate <= 0 {
			if c.powerLimit > 0 {
				rate = c.powerLimit
			} else {
				return fmt.Errorf("no %s rate configured", c.dir.Name)
			}
		}
		if rate < 0 {
			rate = 0
		}
		if rate <= 0 {
			return fmt.Errorf("invalid %s rate", c.dir.Name)
		}

		// Honor the SoC limit even for manual starts so a remote command cannot
		// over-charge or over-discharge the battery past its configured boundary.
		if c.status != nil && c.dir.GoalReached(c.soc, c.socLimit) {
			return fmt.Errorf("battery already at SoC limit (%.0f%%); not starting %s", c.socLimit, c.dir.Name)
		}

		c.rate = rate
		c.manualOverride = true
		c.overrideSource = cmd.Source
		c.ready = true

		if err := c.switchToManual(); err != nil {
			return fmt.Errorf("switching to manual mode: %w", err)
		}

		log.With(
			slog.Int("rate", c.rate),
			slog.String("source", c.overrideSource),
		).Info("starting " + c.dir.Name + " via remote command")
		if err := c.dir.StartOp(c.rate); err != nil {
			return fmt.Errorf("starting %s: %w", c.dir.Name, err)
		}

		c.active = true
		return nil

	case CommandStop:
		c.manualOverride = false
		c.overrideSource = ""
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
		switch cmd.Mode {
		case OperatingModeManual:
			c.manualOverride = true
			c.overrideSource = cmd.Source
			if err := c.switchToManual(); err != nil {
				return fmt.Errorf("forcing manual mode: %w", err)
			}
			log.Info("forced manual mode via remote command")
			return nil
		case OperatingModeAuto:
			c.manualOverride = false
			c.overrideSource = ""
			if err := c.switchToAuto(); err != nil {
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

		// An override driven by an external event survives the config update:
		// the event, not the config, decides when it ends. The server pushes
		// config on every auto-schedule change, so clearing it here would cut a
		// running EV charging session off from the battery.
		if c.manualOverride {
			if c.overrideSource != "" {
				log.With(slog.String("source", c.overrideSource)).
					Info("config update received, keeping externally driven override")
			} else {
				log.Info("config update received, clearing manual override mode")
				c.manualOverride = false
			}
		}

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
		if c.manualOverride {
			// Mirror evaluate(): an override owns the limits and the rate, so
			// re-deriving them from the schedules here would reset a running
			// operation to the battery defaults on the next poll.
			c.ready = true
			c.deadbandHold = false
		} else {
			c.checkTime()
		}

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

// calculateRate sets the operation rate to the power limit from the schedule,
// clamped to a non-negative value so an invalid limit never reaches the hardware.
func (c *Controller) calculateRate() {
	c.rate = c.powerLimit
	if c.rate < 0 {
		c.rate = 0
	}
}

// observeStatus records the latest reading into the controller's internal state used by
// the operation logic. Battery-level and direction gauges are emitted by the shared
// StatusPoller, so the controller no longer writes them here.
func (c *Controller) observeStatus(status *entity.SystemStatus) {
	if status == nil {
		return
	}

	c.status = status
	c.soc = status.USOC
	c.capacity = status.RemainingCapacityWh
}

// invalidateStatus drops the cached reading once the poller reports the battery has
// been unreachable long enough that the reading is no longer evidence of anything.
// Keeping it would let the controller start or stop an operation from data that is
// minutes old; instead it goes quiet until a fresh reading arrives, and that first
// reading re-syncs internal state, since the battery may have been changed by
// someone else while it was unreachable.
func (c *Controller) invalidateStatus() {
	if c.status == nil {
		return
	}

	c.log.With(
		slog.Bool("is_active", c.active),
	).Warn("battery unreachable, discarding stale status until a fresh reading arrives")

	c.status = nil
	c.ready = false
	c.deadbandHold = false
	c.firstStatusPoll = true
}

// syncStateFromBattery synchronizes internal state with actual battery state on startup.
func (c *Controller) syncStateFromBattery(status *entity.SystemStatus) {
	if status == nil {
		return
	}

	// Whether the running operation is one this controller has no record of starting.
	// True at startup, where the process has just lost all state, and false when
	// re-syncing after an outage, where c.active still says the operation is ours.
	unowned := !c.active

	// If battery is in manual mode and operation is active, sync our internal state
	// OperatingMode "1" = manual, "2" = auto
	if status.OperatingMode == batteryModeManual && c.dir.IsActive(status) {
		if unowned {
			c.log.With(
				slog.String("operating_mode", status.OperatingMode),
				slog.Bool("battery_active", c.dir.IsActive(status)),
			).Info("detected an ongoing " + c.dir.Name + " in manual mode on startup, syncing internal state")
			c.active = true
		}

		// Register the adopted operation with the coordinator so the opposite
		// direction will not switch the battery back to auto underneath us.
		if c.mode != nil {
			c.mode.MarkActive(c.dir.Name)
		}

		// If no schedule currently justifies this operation, it was started manually
		// (e.g. a remote start command before this agent restarted). Preserve it as a
		// manual override; otherwise the next tick would stop it and return to auto.
		// Only for an operation this controller does not already own: after an outage
		// a schedule-driven discharge whose window closed meanwhile must be stopped,
		// not promoted to a manual override that would never end.
		if unowned && !c.manualOverride && !c.hasActiveSchedule() {
			c.log.Info("detected manual " + c.dir.Name + " with no active schedule on startup, preserving manual override")
			c.manualOverride = true
			c.ready = true
		}
	}

	// If battery is in auto mode but we think we're active (stale state), clear it
	if status.OperatingMode == batteryModeAuto && c.active && !c.manualOverride {
		c.log.Info("battery in auto mode but internal state shows " + c.dir.Name + "ing, clearing stale state")
		c.active = false
	}
}
