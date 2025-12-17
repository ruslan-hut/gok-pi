package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	apiclient "gok-pi/battery/api-client"
	"gok-pi/battery/charger"
	"gok-pi/battery/discharger"
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
	goalReached := config.GetScheduleGoalReached()
	manager.Apply(ctx, &wg, batteries, schedules, conf.Timezone, goalReached, lg)

	if conf.RemoteControl.Enabled {
		lg.Info("starting remote control client", slog.String("url", conf.RemoteControl.ServerURL))
		// Use DeviceID from config, or empty string to fallback to hostname in wsclient.New
		remoteClient := wsclient.New(conf.RemoteControl, wsclient.AgentMetadata{
			ID:  conf.DeviceID,
			Env: conf.Env,
		}, lg)
		remoteClient.Run(ctx)
		remoteClient.PublishConfigSnapshot(conf.Batteries, conf.Schedules)
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
					goalReached := config.GetScheduleGoalReached()
					manager.Apply(ctx, &wg, filterEnabledBatteries(update.Config.Batteries), filterEnabledSchedules(update.Config.Schedules), update.Config.Timezone, goalReached, lg)

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

			// Route charge commands to charger, discharge commands to discharger
			if isChargeCommand(cmd.Command) {
				chargerWorker, ok := manager.GetCharger(cmd.Target)
				if !ok {
					log.With(slog.String("target", cmd.Target)).Warn("remote charge command for unknown battery")
					continue
				}
				controlCmd, err := translateChargeCommand(cmd)
				if err != nil {
					log.With(slog.String("target", cmd.Target), sl.Err(err)).Warn("failed to translate remote charge command")
					continue
				}
				if err := chargerWorker.SubmitCommand(controlCmd); err != nil {
					log.With(slog.String("target", cmd.Target), sl.Err(err)).Error("failed to submit remote charge command")
				}
			} else {
				dischargerWorker, ok := manager.GetDischarger(cmd.Target)
				if !ok {
					log.With(slog.String("target", cmd.Target)).Warn("remote command for unknown battery")
					continue
				}
				controlCmd, err := translateCommand(cmd)
				if err != nil {
					log.With(slog.String("target", cmd.Target), sl.Err(err)).Warn("failed to translate remote command")
					continue
				}
				if err := dischargerWorker.SubmitCommand(controlCmd); err != nil {
					log.With(slog.String("target", cmd.Target), sl.Err(err)).Error("failed to submit remote command")
				}
			}
		}
	}
}

func isChargeCommand(cmd string) bool {
	return cmd == string(charger.CommandStartCharge) || cmd == string(charger.CommandStopCharge)
}

type workerEntry struct {
	dischargerWorker *discharger.Discharge
	chargerWorker    *charger.Charger
	config           entity.BatteryConfig
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

func (m *workerManager) GetDischarger(name string) (*discharger.Discharge, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.workers[name]
	if !ok {
		return nil, false
	}
	return entry.dischargerWorker, true
}

func (m *workerManager) GetCharger(name string) (*charger.Charger, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.workers[name]
	if !ok {
		return nil, false
	}
	return entry.chargerWorker, true
}

func (m *workerManager) Apply(ctx context.Context, wg *sync.WaitGroup, batteries []entity.BatteryConfig, schedules []entity.Schedule, timezone string, goalReached map[string]time.Time, log *slog.Logger) {
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
			if removed.dischargerWorker != nil {
				go removed.dischargerWorker.Stop()
			}
			if removed.chargerWorker != nil {
				go removed.chargerWorker.Stop()
			}
		}
	}

	// Upsert desired workers.
	for name, cfg := range desired {
		entry, ok := m.getEntry(name)
		if ok {
			// Check if critical fields (URL or token) have changed, which require worker restart
			urlChanged := entry.config.Url != cfg.Url
			tokenChanged := entry.config.Token != cfg.Token

			// If URL or token changed, we must restart workers to use the new ApiClient
			if urlChanged || tokenChanged {
				log.With(
					slog.String("battery", name),
					slog.Bool("url_changed", urlChanged),
					slog.Bool("token_changed", tokenChanged),
				).Info("restarting workers (URL or token changed)")
				if removed, ok := m.remove(name); ok {
					if removed.dischargerWorker != nil {
						go removed.dischargerWorker.Stop()
					}
					if removed.chargerWorker != nil {
						go removed.chargerWorker.Stop()
					}
				}
			} else if entry.config == cfg {
				// Config is identical, just update schedules/timezone for existing workers
				timezonePtr := &timezone
				if timezone == "" {
					timezonePtr = nil
				}
				if entry.dischargerWorker != nil {
					_ = entry.dischargerWorker.SubmitCommand(discharger.ControlCommand{
						Type: discharger.CommandUpdateConfig,
						Config: &discharger.ConfigUpdate{
							Schedules: scheduleByBattery[name],
							Timezone:  timezonePtr,
						},
					})
				}
				if entry.chargerWorker != nil {
					_ = entry.chargerWorker.SubmitCommand(charger.ControlCommand{
						Type: charger.CommandUpdateConfig,
						Config: &charger.ConfigUpdate{
							Schedules: scheduleByBattery[name],
							Timezone:  timezonePtr,
						},
					})
				}
				continue
			} else {
				// Other config fields changed (not URL/token), restart workers
				if removed, ok := m.remove(name); ok {
					log.With(slog.String("battery", name)).Info("restarting workers (config changed)")
					if removed.dischargerWorker != nil {
						go removed.dischargerWorker.Stop()
					}
					if removed.chargerWorker != nil {
						go removed.chargerWorker.Stop()
					}
				}
			}
		}

		entry, err := startWorker(ctx, wg, cfg, scheduleByBattery[name], timezone, goalReached, log)
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

func startWorker(ctx context.Context, wg *sync.WaitGroup, battery entity.BatteryConfig, schedules []entity.Schedule, timezone string, goalReached map[string]time.Time, log *slog.Logger) (*workerEntry, error) {
	workerLog := log.With(slog.String("battery", battery.Name))
	api := apiclient.New(battery.Url, battery.Token, workerLog)

	entry := &workerEntry{
		config: battery,
	}

	// Always create a monitor worker to track battery status and emit data to control server
	// This ensures enabled batteries are monitored even without schedules
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

	// Create discharger worker if we have discharge schedules
	if hasDischargeSchedules {
		dischargerWorker, err := discharger.New(battery.Name, api, workerLog)
		if err != nil {
			return nil, fmt.Errorf("creating discharge worker: %w", err)
		}

		for _, schedule := range schedules {
			dischargerWorker.AddSchedule(schedule)
		}

		dischargerWorker.SetCapacityLimit(battery.CapacityLimit)
		// Set timezone if provided
		if timezone != "" {
			if err := dischargerWorker.SetTimezone(timezone); err != nil {
				workerLog.With(sl.Err(err)).Warn("failed to set timezone for discharger")
			}
		}
		// Initialize battery default limits from battery config (used when no schedule is active)
		if battery.PowerLimit > 0 || battery.SocLimit > 0 {
			powerLimit := battery.PowerLimit
			socLimit := battery.SocLimit
			if powerLimit == 0 {
				powerLimit = 1000 // Default if not set
			}
			if socLimit == 0 {
				socLimit = 50 // Default if not set
			}
			dischargerWorker.SetBatteryDefaults(powerLimit, socLimit)
		}
		// Set goal reached state and callbacks
		dischargerWorker.SetGoalReachedState(goalReached, config.UpdateScheduleGoalReached, config.ClearScheduleGoalReached)
		entry.dischargerWorker = dischargerWorker

		wg.Add(1)
		go func() {
			defer wg.Done()

			stopCh := make(chan struct{})
			go func() {
				select {
				case <-ctx.Done():
					dischargerWorker.Stop()
				case <-stopCh:
				}
			}()

			if err := dischargerWorker.Run(); err != nil {
				workerLog.Error("running discharge worker", sl.Err(err))
			}
			workerLog.Info("discharge worker stopped")
			close(stopCh)
		}()
	}

	// Create charger worker if we have charge schedules
	if hasChargeSchedules {
		chargerWorker, err := charger.New(battery.Name, api, workerLog)
		if err != nil {
			// If discharger was created, we should still return it
			if entry.dischargerWorker != nil {
				entry.dischargerWorker.Stop()
			}
			return nil, fmt.Errorf("creating charge worker: %w", err)
		}

		for _, schedule := range schedules {
			chargerWorker.AddSchedule(schedule)
		}

		chargerWorker.SetCapacityLimit(battery.CapacityLimit)
		// Set timezone if provided
		if timezone != "" {
			if err := chargerWorker.SetTimezone(timezone); err != nil {
				workerLog.With(sl.Err(err)).Warn("failed to set timezone for charger")
			}
		}
		// Initialize battery default limits from battery config (used when no schedule is active)
		if battery.PowerLimit > 0 || battery.SocLimit > 0 {
			powerLimit := battery.PowerLimit
			socLimit := battery.SocLimit
			if powerLimit == 0 {
				powerLimit = 1000 // Default if not set
			}
			if socLimit == 0 {
				socLimit = 50 // Default if not set
			}
			chargerWorker.SetBatteryDefaults(powerLimit, socLimit)
		}
		// Set goal reached state and callbacks
		chargerWorker.SetGoalReachedState(goalReached, config.UpdateScheduleGoalReached, config.ClearScheduleGoalReached)
		entry.chargerWorker = chargerWorker

		wg.Add(1)
		go func() {
			defer wg.Done()

			stopCh := make(chan struct{})
			go func() {
				select {
				case <-ctx.Done():
					chargerWorker.Stop()
				case <-stopCh:
				}
			}()

			if err := chargerWorker.Run(); err != nil {
				workerLog.Error("running charge worker", sl.Err(err))
			}
			workerLog.Info("charge worker stopped")
			close(stopCh)
		}()
	}

	// Initial status is set by monitorBattery function
	// No need to set it here as monitor starts immediately

	return entry, nil
}

// monitorBattery continuously monitors battery status and emits data to observers
// This ensures enabled batteries are always monitored, even without schedules
func monitorBattery(ctx context.Context, name string, api *apiclient.ApiClient, log *slog.Logger) {
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

func translateCommand(cmd wsclient.Command) (discharger.ControlCommand, error) {
	switch cmd.Command {
	case string(discharger.CommandStartDischarge):
		var payload struct {
			Power int `json:"power"`
		}
		if len(cmd.Payload) > 0 {
			if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
				return discharger.ControlCommand{}, fmt.Errorf("decode start payload: %w", err)
			}
		}
		return discharger.ControlCommand{
			Type:  discharger.CommandStartDischarge,
			Power: payload.Power,
		}, nil
	case string(discharger.CommandStopDischarge):
		return discharger.ControlCommand{
			Type: discharger.CommandStopDischarge,
		}, nil
	case string(discharger.CommandSetLimits):
		var payload struct {
			PowerLimit *int `json:"power_limit"`
			SocLimit   *int `json:"soc_limit"`
		}
		if len(cmd.Payload) > 0 {
			if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
				return discharger.ControlCommand{}, fmt.Errorf("decode limits payload: %w", err)
			}
		}
		return discharger.ControlCommand{
			Type: discharger.CommandSetLimits,
			Limits: &discharger.CommandLimits{
				PowerLimit: payload.PowerLimit,
				SocLimit:   payload.SocLimit,
			},
		}, nil
	case string(discharger.CommandForceMode):
		var payload struct {
			Mode string `json:"mode"`
		}
		if len(cmd.Payload) > 0 {
			if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
				return discharger.ControlCommand{}, fmt.Errorf("decode force mode payload: %w", err)
			}
		}
		mode := strings.ToLower(payload.Mode)
		switch mode {
		case "manual":
			return discharger.ControlCommand{
				Type: discharger.CommandForceMode,
				Mode: discharger.OperatingModeManual,
			}, nil
		case "auto", "":
			return discharger.ControlCommand{
				Type: discharger.CommandForceMode,
				Mode: discharger.OperatingModeAuto,
			}, nil
		default:
			return discharger.ControlCommand{}, fmt.Errorf("unsupported operating mode: %s", mode)
		}
	case string(discharger.CommandResetGoal):
		var payload struct {
			ScheduleName string `json:"schedule_name"`
		}
		if len(cmd.Payload) > 0 {
			if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
				return discharger.ControlCommand{}, fmt.Errorf("decode reset_goal payload: %w", err)
			}
		}
		if payload.ScheduleName == "" {
			return discharger.ControlCommand{}, fmt.Errorf("schedule_name is required for reset_goal command")
		}
		return discharger.ControlCommand{
			Type:         discharger.CommandResetGoal,
			ScheduleName: payload.ScheduleName,
		}, nil
	default:
		return discharger.ControlCommand{}, fmt.Errorf("unsupported remote command: %s", cmd.Command)
	}
}

func translateChargeCommand(cmd wsclient.Command) (charger.ControlCommand, error) {
	switch cmd.Command {
	case string(charger.CommandStartCharge):
		var payload struct {
			Power int `json:"power"`
		}
		if len(cmd.Payload) > 0 {
			if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
				return charger.ControlCommand{}, fmt.Errorf("decode start payload: %w", err)
			}
		}
		return charger.ControlCommand{
			Type:  charger.CommandStartCharge,
			Power: payload.Power,
		}, nil
	case string(charger.CommandStopCharge):
		return charger.ControlCommand{
			Type: charger.CommandStopCharge,
		}, nil
	case string(charger.CommandSetLimits):
		var payload struct {
			PowerLimit *int `json:"power_limit"`
			SocLimit   *int `json:"soc_limit"`
		}
		if len(cmd.Payload) > 0 {
			if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
				return charger.ControlCommand{}, fmt.Errorf("decode limits payload: %w", err)
			}
		}
		return charger.ControlCommand{
			Type: charger.CommandSetLimits,
			Limits: &charger.CommandLimits{
				PowerLimit: payload.PowerLimit,
				SocLimit:   payload.SocLimit,
			},
		}, nil
	case string(charger.CommandForceMode):
		var payload struct {
			Mode string `json:"mode"`
		}
		if len(cmd.Payload) > 0 {
			if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
				return charger.ControlCommand{}, fmt.Errorf("decode force mode payload: %w", err)
			}
		}
		mode := strings.ToLower(payload.Mode)
		switch mode {
		case "manual":
			return charger.ControlCommand{
				Type: charger.CommandForceMode,
				Mode: charger.OperatingModeManual,
			}, nil
		case "auto", "":
			return charger.ControlCommand{
				Type: charger.CommandForceMode,
				Mode: charger.OperatingModeAuto,
			}, nil
		default:
			return charger.ControlCommand{}, fmt.Errorf("unsupported operating mode: %s", mode)
		}
	case string(charger.CommandResetGoal):
		var payload struct {
			ScheduleName string `json:"schedule_name"`
		}
		if len(cmd.Payload) > 0 {
			if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
				return charger.ControlCommand{}, fmt.Errorf("decode reset_goal payload: %w", err)
			}
		}
		if payload.ScheduleName == "" {
			return charger.ControlCommand{}, fmt.Errorf("schedule_name is required for reset_goal command")
		}
		return charger.ControlCommand{
			Type:         charger.CommandResetGoal,
			ScheduleName: payload.ScheduleName,
		}, nil
	default:
		return charger.ControlCommand{}, fmt.Errorf("unsupported remote command: %s", cmd.Command)
	}
}
