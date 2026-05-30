// Agent (gok) is the primary battery controller daemon that runs on-device
// (typically a Raspberry Pi) near the Sonnen battery system.
//
// Startup flow:
//  1. Load config.yml -> filter enabled batteries and schedules
//  2. Create a discharge + charge controller pair per battery (via battery/controller)
//  3. Start Prometheus metrics server (if enabled)
//  4. Connect to control server via WebSocket (if remote_control enabled)
//  5. Enter main loop: poll battery status every 10s, execute schedules, handle commands
//
// The agent supports graceful shutdown via SIGINT/SIGTERM, stopping all workers
// and closing the WebSocket connection cleanly.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"gok-pi/battery/controller"
	"gok-pi/battery/driver"
	_ "gok-pi/battery/driver/sonnen"
	"gok-pi/battery/entity"
	"gok-pi/internal/config"
	"gok-pi/internal/lib/logger"
	"gok-pi/internal/lib/sl"
	"gok-pi/internal/remote/wsclient"
	"gok-pi/metrics/observers"
	"gok-pi/metrics/server"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	// Embed the IANA timezone database so schedule timezones resolve even on
	// minimal Raspberry Pi images that ship no system zoneinfo (otherwise summer
	// schedules silently shift by one hour).
	_ "time/tzdata"
)

func main() {

	configPath := flag.String("conf", "config.yml", "path to config file")
	logPath := flag.String("log", "/var/log", "path to log file directory")
	flag.Parse()

	conf := config.MustLoad(*configPath)
	lg := logger.SetupLogger(conf.Env, *logPath)

	lg.Info("starting gok-pi",
		slog.String("config", *configPath),
		slog.String("env", conf.Env),
		slog.String("device_id", conf.DeviceID))
	lg.Debug("debug messages enabled")
	// filter enabled batteries
	var batteries []entity.BatteryConfig
	for _, b := range conf.Batteries {
		if b.Enabled {
			batteries = append(batteries, b)
		}
	}
	lg.With(
		slog.Int("batteries", len(batteries)),
	).Info("loaded batteries config")

	if len(batteries) == 0 {
		lg.Warn("no batteries enabled; agent will still connect if remote control is enabled")
	}

	// filter enabled schedules
	var schedules []entity.Schedule
	for _, s := range conf.Schedules {
		if s.Enabled {
			schedules = append(schedules, s)
		}
	}
	lg.With(
		slog.Int("schedules", len(schedules)),
	).Info("loaded schedules")

	if conf.Metrics.Enabled {
		lg.Info("starting metrics server", slog.String("bind", conf.Metrics.Bind), slog.String("port", conf.Metrics.Port))
		go func() {
			err := server.Listen(conf.Metrics.Bind, conf.Metrics.Port)
			if err != nil {
				lg.Error("metrics server", sl.Err(err))
				return
			}
		}()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		lg.Info("received signal, shutting down", slog.String("signal", sig.String()))
		cancel()
	}()

	// Set status for all batteries (including disabled ones) before starting workers
	for _, b := range conf.Batteries {
		if !b.Enabled {
			observers.UpdateStatus(b.Name, "Disabled")
		}
	}

	var wg sync.WaitGroup
	manager := newWorkerManager()
	manager.Apply(ctx, &wg, batteries, schedules, conf.Timezone, lg)

	if conf.RemoteControl.Enabled {
		lg.Info("starting remote control client", slog.String("url", conf.RemoteControl.ServerURL))
		// Use DeviceID from config, or empty string to fallback to hostname in wsclient.New
		remoteClient := wsclient.New(conf.RemoteControl, wsclient.AgentMetadata{
			ID:  conf.DeviceID,
			Env: conf.Env,
		}, lg)
		remoteClient.Run(ctx)
		remoteClient.PublishConfigSnapshot(conf.Batteries, conf.Schedules)

		// Set up callback to push config updates when goal state changes
		config.SetGoalStateChangedCallback(func() {
			remoteClient.PublishConfigSnapshot(config.GetBatteries(), config.GetSchedules())
		})

		go handleRemoteCommands(ctx, remoteClient.Commands(), manager, lg)

		go func() {
			var lastRevision int
			for {
				select {
				case <-ctx.Done():
					return
				case update, ok := <-remoteClient.ConfigUpdates():
					if !ok {
						return
					}
					if update.Config.Revision <= lastRevision {
						continue
					}
					lastRevision = update.Config.Revision
					lg.With(
						slog.Int("revision", update.Config.Revision),
						slog.Time("sent_at", update.SentAt),
					).Info("applying remote configuration")

					// Set status for all batteries (including disabled ones) before applying config
					for _, b := range update.Config.Batteries {
						if !b.Enabled {
							observers.UpdateStatus(b.Name, "Disabled")
						}
					}
					manager.Apply(ctx, &wg, filterEnabledBatteries(update.Config.Batteries), filterEnabledSchedules(update.Config.Schedules), update.Config.Timezone, lg)

					// Persist remote configuration to local config.yml
					config.UpdateFromRemoteConfig(update.Config.DeviceName, update.Config.Env, update.Config.Timezone, update.Config.Batteries, update.Config.Schedules)
					if err := config.Save(); err != nil {
						lg.With(
							slog.Int("revision", update.Config.Revision),
							sl.Err(err),
						).Warn("failed to persist remote configuration to local config file")
					} else {
						lg.With(
							slog.Int("revision", update.Config.Revision),
						).Info("persisted remote configuration to local config file")
					}
				}
			}
		}()

		// If remote control is enabled, keep the agent running even without batteries or schedules
		// Wait for context cancellation (e.g., SIGINT/SIGTERM)
		lg.Info("agent running with remote control enabled; waiting for context cancellation")
		<-ctx.Done()
	} else {
		// If no remote control and no batteries, exit immediately
		if len(batteries) == 0 {
			lg.Warn("no batteries enabled and remote control disabled; exiting")
			return
		}
		wg.Wait()
	}

	lg.Info("gok-pi stopped")
}

// Wire-format command strings used by the WebSocket protocol.
const (
	wireStartDischarge = "start_discharge"
	wireStopDischarge  = "stop_discharge"
	wireStartCharge    = "start_charge"
	wireStopCharge     = "stop_charge"
	wireSetLimits      = "set_limits"
	wireForceMode      = "force_mode"
	wireResetGoal      = "reset_goal"
)

func handleRemoteCommands(ctx context.Context, commands <-chan wsclient.Command, manager *workerManager, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd, ok := <-commands:
			if !ok {
				return
			}
			if cmd.Type != "agent.command" {
				continue
			}
			if cmd.Target == "" {
				log.Warn("remote command missing target battery")
				continue
			}

			// Handle reset_goal command specially - needs to route to correct worker based on schedule type
			if cmd.Command == wireResetGoal {
				handleResetGoalCommand(cmd, manager, log)
				continue
			}

			// Route charge commands to charger, discharge commands to discharger
			if isChargeCommand(cmd.Command) {
				worker, ok := manager.GetCharger(cmd.Target)
				if !ok {
					log.With(slog.String("target", cmd.Target)).Warn("remote charge command for unknown battery")
					continue
				}
				controlCmd, err := translateCommand(cmd)
				if err != nil {
					log.With(slog.String("target", cmd.Target), sl.Err(err)).Warn("failed to translate remote charge command")
					continue
				}
				if err := worker.SubmitCommand(controlCmd); err != nil {
					log.With(slog.String("target", cmd.Target), sl.Err(err)).Error("failed to submit remote charge command")
				}
			} else {
				worker, ok := manager.GetDischarger(cmd.Target)
				if !ok {
					log.With(slog.String("target", cmd.Target)).Warn("remote command for unknown battery")
					continue
				}
				controlCmd, err := translateCommand(cmd)
				if err != nil {
					log.With(slog.String("target", cmd.Target), sl.Err(err)).Warn("failed to translate remote command")
					continue
				}
				if err := worker.SubmitCommand(controlCmd); err != nil {
					log.With(slog.String("target", cmd.Target), sl.Err(err)).Error("failed to submit remote command")
				}
			}
		}
	}
}

// handleResetGoalCommand routes reset_goal command to the correct worker (charger or discharger)
// based on the schedule type.
func handleResetGoalCommand(cmd wsclient.Command, manager *workerManager, log *slog.Logger) {
	// Parse payload to get schedule name
	var payload struct {
		ScheduleName string `json:"schedule_name"`
	}
	if len(cmd.Payload) > 0 {
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			log.With(slog.String("target", cmd.Target), sl.Err(err)).Warn("failed to decode reset_goal payload")
			return
		}
	}
	if payload.ScheduleName == "" {
		log.With(slog.String("target", cmd.Target)).Warn("reset_goal command missing schedule_name")
		return
	}

	// Look up schedule type to determine which worker to route to
	scheduleType, found := manager.GetScheduleType(cmd.Target, payload.ScheduleName)
	if !found {
		log.With(
			slog.String("target", cmd.Target),
			slog.String("schedule", payload.ScheduleName),
		).Warn("reset_goal command for unknown schedule")
		return
	}

	controlCmd, err := translateCommand(cmd)
	if err != nil {
		log.With(slog.String("target", cmd.Target), sl.Err(err)).Warn("failed to translate reset_goal command")
		return
	}

	// Route to appropriate worker based on schedule type
	if scheduleType == "charge" {
		worker, ok := manager.GetCharger(cmd.Target)
		if !ok {
			log.With(slog.String("target", cmd.Target)).Warn("reset_goal command: charger worker not found")
			return
		}
		if err := worker.SubmitCommand(controlCmd); err != nil {
			log.With(slog.String("target", cmd.Target), sl.Err(err)).Error("failed to submit reset_goal command to charger")
		}
	} else {
		// Discharge schedule (type is "" or "discharge")
		worker, ok := manager.GetDischarger(cmd.Target)
		if !ok {
			log.With(slog.String("target", cmd.Target)).Warn("reset_goal command: discharger worker not found")
			return
		}
		if err := worker.SubmitCommand(controlCmd); err != nil {
			log.With(slog.String("target", cmd.Target), sl.Err(err)).Error("failed to submit reset_goal command to discharger")
		}
	}
}

func isChargeCommand(cmd string) bool {
	return cmd == wireStartCharge || cmd == wireStopCharge
}

type workerEntry struct {
	discharger *controller.Controller
	charger    *controller.Controller
	config     entity.BatteryConfig
}

type workerManager struct {
	mu      sync.RWMutex
	workers map[string]*workerEntry
}

func newWorkerManager() *workerManager {
	return &workerManager{
		workers: make(map[string]*workerEntry),
	}
}

func (m *workerManager) GetDischarger(name string) (*controller.Controller, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.workers[name]
	if !ok {
		return nil, false
	}
	return entry.discharger, true
}

func (m *workerManager) GetCharger(name string) (*controller.Controller, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.workers[name]
	if !ok {
		return nil, false
	}
	return entry.charger, true
}

// GetScheduleType looks up a schedule by name in the workers for a given battery
// and returns its type. Returns "charge" for charge schedules, "" or "discharge" for discharge schedules.
func (m *workerManager) GetScheduleType(batteryName, scheduleName string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.workers[batteryName]
	if !ok {
		return "", false
	}

	// Check charger first (since we need to identify charge schedules specifically)
	if entry.charger != nil {
		if schedType, found := entry.charger.GetScheduleType(scheduleName); found {
			return schedType, true
		}
	}

	// Check discharger
	if entry.discharger != nil {
		if schedType, found := entry.discharger.GetScheduleType(scheduleName); found {
			return schedType, true
		}
	}

	return "", false
}

func (m *workerManager) Apply(ctx context.Context, wg *sync.WaitGroup, batteries []entity.BatteryConfig, schedules []entity.Schedule, timezone string, log *slog.Logger) {
	desired := make(map[string]entity.BatteryConfig)
	allBatteries := make(map[string]entity.BatteryConfig)
	for _, b := range batteries {
		allBatteries[b.Name] = b
		if b.Enabled {
			desired[b.Name] = b
		} else {
			// Set status to Disabled for disabled batteries
			observers.UpdateStatus(b.Name, "Disabled")
		}
	}

	scheduleByBattery := groupSchedules(schedules)

	current := m.snapshot()

	// Remove workers that are no longer desired.
	for name := range current {
		if _, ok := desired[name]; ok {
			continue
		}
		if removed, ok := m.remove(name); ok {
			log.With(slog.String("battery", name)).Info("stopping workers (no longer configured)")
			// Set status to Disabled if battery is disabled, otherwise it will be set when removed from config
			if battery, exists := allBatteries[name]; exists && !battery.Enabled {
				observers.UpdateStatus(name, "Disabled")
			}
			if removed.discharger != nil {
				go removed.discharger.Stop()
			}
			if removed.charger != nil {
				go removed.charger.Stop()
			}
		}
	}

	// Upsert desired workers.
	for name, cfg := range desired {
		entry, ok := m.getEntry(name)
		if ok {
			// Check if critical fields (URL, token, or driver) have changed, which require worker restart
			urlChanged := entry.config.Url != cfg.Url
			tokenChanged := entry.config.Token != cfg.Token
			driverChanged := entry.config.Driver != cfg.Driver

			// If URL, token, or driver changed, we must restart workers with a new driver instance
			if urlChanged || tokenChanged || driverChanged {
				log.With(
					slog.String("battery", name),
					slog.Bool("url_changed", urlChanged),
					slog.Bool("token_changed", tokenChanged),
					slog.Bool("driver_changed", driverChanged),
				).Info("restarting workers (connection config changed)")
				if removed, ok := m.remove(name); ok {
					if removed.discharger != nil {
						removed.discharger.Stop()
					}
					if removed.charger != nil {
						removed.charger.Stop()
					}
				}
			} else if entry.config == cfg {
				// Config is identical, just update schedules/timezone for existing workers
				timezonePtr := &timezone
				if timezone == "" {
					timezonePtr = nil
				}
				configUpdate := controller.ControlCommand{
					Type: controller.CommandUpdateConfig,
					Config: &controller.ConfigUpdate{
						Schedules: scheduleByBattery[name],
						Timezone:  timezonePtr,
					},
				}
				if entry.discharger != nil {
					_ = entry.discharger.SubmitCommand(configUpdate)
				}
				if entry.charger != nil {
					_ = entry.charger.SubmitCommand(configUpdate)
				}
				continue
			} else {
				// Other config fields changed (not URL/token), restart workers
				if removed, ok := m.remove(name); ok {
					log.With(slog.String("battery", name)).Info("restarting workers (config changed)")
					if removed.discharger != nil {
						removed.discharger.Stop()
					}
					if removed.charger != nil {
						removed.charger.Stop()
					}
				}
			}
		}

		entry, err := startWorker(ctx, wg, cfg, scheduleByBattery[name], timezone, log)
		if err != nil {
			log.With(slog.String("battery", name), sl.Err(err)).Error("starting workers")
			continue
		}
		m.set(name, entry)
	}
}

func (m *workerManager) snapshot() map[string]*workerEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]*workerEntry, len(m.workers))
	for name, entry := range m.workers {
		out[name] = entry
	}
	return out
}

func (m *workerManager) getEntry(name string) (*workerEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.workers[name]
	return entry, ok
}

func (m *workerManager) set(name string, entry *workerEntry) {
	m.mu.Lock()
	m.workers[name] = entry
	m.mu.Unlock()
}

func (m *workerManager) remove(name string) (*workerEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.workers[name]
	if ok {
		delete(m.workers, name)
	}
	return entry, ok
}

// initController creates and configures a controller for the given direction.
func initController(battery entity.BatteryConfig, schedules []entity.Schedule, timezone string, dir controller.Direction, client controller.Client, mode *controller.ModeCoordinator, workerLog *slog.Logger) (*controller.Controller, error) {
	w, err := controller.New(battery.Name, client, dir, workerLog)
	if err != nil {
		return nil, err
	}
	w.SetModeCoordinator(mode)

	for _, schedule := range schedules {
		w.AddSchedule(schedule)
	}

	w.SetCapacityLimit(battery.CapacityLimit)
	if timezone != "" {
		if err := w.SetTimezone(timezone); err != nil {
			workerLog.With(sl.Err(err)).Warn("failed to set timezone for " + dir.Name)
		}
	}
	if battery.PowerLimit > 0 || battery.SocLimit > 0 {
		powerLimit := battery.PowerLimit
		socLimit := battery.SocLimit
		if powerLimit == 0 {
			powerLimit = 1000
		}
		if socLimit == 0 {
			socLimit = 50
		}
		w.SetBatteryDefaults(powerLimit, socLimit)
	}
	w.SetGoalCallbacks(config.UpdateScheduleGoalReached, config.ClearScheduleGoalReached)
	w.SetRemoveScheduleCallback(config.RemoveSchedule)
	return w, nil
}

// runController runs the controller in a goroutine with context cancellation support.
func runController(ctx context.Context, wg *sync.WaitGroup, w *controller.Controller, label string, workerLog *slog.Logger) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		stopCh := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				w.Stop()
			case <-stopCh:
			}
		}()

		if err := w.Run(); err != nil {
			workerLog.Error("running "+label+" worker", sl.Err(err))
		}
		workerLog.Info(label + " worker stopped")
		close(stopCh)
	}()
}

func startWorker(ctx context.Context, wg *sync.WaitGroup, battery entity.BatteryConfig, schedules []entity.Schedule, timezone string, log *slog.Logger) (*workerEntry, error) {
	workerLog := log.With(slog.String("battery", battery.Name))
	api, err := driver.New(battery.Driver, battery, workerLog)
	if err != nil {
		return nil, fmt.Errorf("creating battery driver: %w", err)
	}

	entry := &workerEntry{
		config: battery,
	}

	// Always create a monitor worker to track battery status and emit data to control server
	wg.Add(1)
	go func() {
		defer wg.Done()
		monitorBattery(ctx, battery.Name, api, workerLog)
	}()

	// Validate and filter schedules
	var validSchedules []entity.Schedule
	for _, schedule := range schedules {
		if err := schedule.Validate(); err != nil {
			workerLog.With(
				slog.String("schedule", schedule.Name),
				sl.Err(err),
			).Warn("skipping invalid schedule")
			continue
		}
		validSchedules = append(validSchedules, schedule)
	}
	schedules = validSchedules

	// Check if we have discharge or charge schedules
	hasDischargeSchedules := false
	hasChargeSchedules := false
	for _, schedule := range schedules {
		if schedule.Type == "charge" {
			hasChargeSchedules = true
		} else {
			hasDischargeSchedules = true
		}
	}

	// Both directions share one mode coordinator so they never fight over the
	// battery's operating mode (auto only when neither direction is active).
	mode := controller.NewModeCoordinator()

	if hasDischargeSchedules {
		w, err := initController(battery, schedules, timezone, controller.DischargeDirection(api), api, mode, workerLog)
		if err != nil {
			return nil, fmt.Errorf("creating discharge worker: %w", err)
		}
		entry.discharger = w
		runController(ctx, wg, w, "discharge", workerLog)
	}

	if hasChargeSchedules {
		w, err := initController(battery, schedules, timezone, controller.ChargeDirection(api), api, mode, workerLog)
		if err != nil {
			if entry.discharger != nil {
				entry.discharger.Stop()
			}
			return nil, fmt.Errorf("creating charge worker: %w", err)
		}
		entry.charger = w
		runController(ctx, wg, w, "charge", workerLog)
	}

	return entry, nil
}

// monitorBattery continuously monitors battery status and emits data to observers
func monitorBattery(ctx context.Context, name string, api driver.Driver, log *slog.Logger) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Set initial status
	observers.UpdateStatus(name, "Disconnected")

	for {
		select {
		case <-ctx.Done():
			log.Info("battery monitor stopped")
			return
		case <-ticker.C:
			status, err := api.Status()
			if err != nil {
				log.With(sl.Err(err)).Error("checking battery status")
				observers.UpdateStatus(name, "Disconnected")
				continue
			}
			observers.UpdateStatus(name, "Connected")
			observeBatteryStatus(name, status)
		}
	}
}

// observeBatteryStatus updates observers with battery status data
func observeBatteryStatus(name string, status *entity.SystemStatus) {
	if status == nil {
		return
	}

	go func() {
		observers.UpdateSoC(name, status.RSOC)
		observers.UpdateUSoC(name, status.USOC)
		observers.UpdateCapacity(name, status.RemainingCapacityWh)
		observers.UpdateConsumption(name, status.ConsumptionW)
		observers.UpdatePac(name, status.PacTotalW)
		observers.UpdateDischargeState(name, status.BatteryDischarging)
		observers.UpdateChargeState(name, status.BatteryCharging)
		observers.UpdateOpMode(name, status.OperatingMode)
	}()
}

func filterEnabledBatteries(batteries []entity.BatteryConfig) []entity.BatteryConfig {
	if len(batteries) == 0 {
		return batteries
	}
	result := make([]entity.BatteryConfig, 0, len(batteries))
	for _, b := range batteries {
		if b.Enabled {
			result = append(result, b)
		}
	}
	return result
}

func filterEnabledSchedules(schedules []entity.Schedule) []entity.Schedule {
	if len(schedules) == 0 {
		return schedules
	}
	result := make([]entity.Schedule, 0, len(schedules))
	for _, s := range schedules {
		if s.Enabled {
			result = append(result, s)
		}
	}
	return result
}

func groupSchedules(schedules []entity.Schedule) map[string][]entity.Schedule {
	result := make(map[string][]entity.Schedule)
	for _, s := range schedules {
		if !s.Enabled {
			continue
		}
		name := s.BatteryName
		result[name] = append(result[name], s)
	}
	return result
}

func translateCommand(cmd wsclient.Command) (controller.ControlCommand, error) {
	switch cmd.Command {
	case wireStartDischarge, wireStartCharge:
		var payload struct {
			Power int `json:"power"`
		}
		if len(cmd.Payload) > 0 {
			if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
				return controller.ControlCommand{}, fmt.Errorf("decode start payload: %w", err)
			}
		}
		return controller.ControlCommand{
			Type:  controller.CommandStart,
			Power: payload.Power,
		}, nil
	case wireStopDischarge, wireStopCharge:
		return controller.ControlCommand{
			Type: controller.CommandStop,
		}, nil
	case wireSetLimits:
		var payload struct {
			PowerLimit *int `json:"power_limit"`
			SocLimit   *int `json:"soc_limit"`
		}
		if len(cmd.Payload) > 0 {
			if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
				return controller.ControlCommand{}, fmt.Errorf("decode limits payload: %w", err)
			}
		}
		return controller.ControlCommand{
			Type: controller.CommandSetLimits,
			Limits: &controller.CommandLimits{
				PowerLimit: payload.PowerLimit,
				SocLimit:   payload.SocLimit,
			},
		}, nil
	case wireForceMode:
		var payload struct {
			Mode string `json:"mode"`
		}
		if len(cmd.Payload) > 0 {
			if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
				return controller.ControlCommand{}, fmt.Errorf("decode force mode payload: %w", err)
			}
		}
		mode := strings.ToLower(payload.Mode)
		switch mode {
		case "manual":
			return controller.ControlCommand{
				Type: controller.CommandForceMode,
				Mode: controller.OperatingModeManual,
			}, nil
		case "auto", "":
			return controller.ControlCommand{
				Type: controller.CommandForceMode,
				Mode: controller.OperatingModeAuto,
			}, nil
		default:
			return controller.ControlCommand{}, fmt.Errorf("unsupported operating mode: %s", mode)
		}
	case wireResetGoal:
		var payload struct {
			ScheduleName string `json:"schedule_name"`
		}
		if len(cmd.Payload) > 0 {
			if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
				return controller.ControlCommand{}, fmt.Errorf("decode reset_goal payload: %w", err)
			}
		}
		if payload.ScheduleName == "" {
			return controller.ControlCommand{}, fmt.Errorf("schedule_name is required for reset_goal command")
		}
		return controller.ControlCommand{
			Type:         controller.CommandResetGoal,
			ScheduleName: payload.ScheduleName,
		}, nil
	default:
		return controller.ControlCommand{}, fmt.Errorf("unsupported remote command: %s", cmd.Command)
	}
}
