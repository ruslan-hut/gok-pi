package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	apiclient "gok-pi/battery/api-client"
	"gok-pi/battery/discharger"
	"gok-pi/battery/entity"
	"gok-pi/internal/config"
	"gok-pi/internal/lib/logger"
	"gok-pi/internal/lib/sl"
	"gok-pi/internal/remote/wsclient"
	"gok-pi/metrics/server"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

func main() {

	configPath := flag.String("conf", "config.yml", "path to config file")
	logPath := flag.String("log", "/var/log", "path to log file directory")
	flag.Parse()

	conf := config.MustLoad(*configPath)
	lg := logger.SetupLogger(conf.Env, *logPath)

	lg.Info("starting gok-pi", slog.String("config", *configPath), slog.String("env", conf.Env))
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

	var wg sync.WaitGroup
	manager := newWorkerManager()
	manager.Apply(ctx, &wg, batteries, schedules, lg)

	if conf.RemoteControl.Enabled {
		lg.Info("starting remote control client", slog.String("url", conf.RemoteControl.ServerURL))
		remoteClient := wsclient.New(conf.RemoteControl, wsclient.AgentMetadata{
			ID:  conf.Env,
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

					manager.Apply(ctx, &wg, filterEnabledBatteries(update.Config.Batteries), filterEnabledSchedules(update.Config.Schedules), lg)
				}
			}
		}()

		// If remote control is enabled, keep the agent running even without batteries
		// Wait for context cancellation (e.g., SIGINT/SIGTERM)
		if len(batteries) == 0 {
			lg.Info("agent running with remote control enabled; waiting for context cancellation")
			<-ctx.Done()
		} else {
			wg.Wait()
		}
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
			worker, ok := manager.Get(cmd.Target)
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

type workerEntry struct {
	worker *discharger.Discharge
	config entity.BatteryConfig
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

func (m *workerManager) Get(name string) (*discharger.Discharge, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.workers[name]
	if !ok {
		return nil, false
	}
	return entry.worker, true
}

func (m *workerManager) Apply(ctx context.Context, wg *sync.WaitGroup, batteries []entity.BatteryConfig, schedules []entity.Schedule, log *slog.Logger) {
	desired := make(map[string]entity.BatteryConfig)
	for _, b := range batteries {
		if b.Enabled {
			desired[b.Name] = b
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
			log.With(slog.String("battery", name)).Info("stopping discharge worker (no longer configured)")
			go removed.worker.Stop()
		}
	}

	// Upsert desired workers.
	for name, cfg := range desired {
		entry, ok := m.getEntry(name)
		if ok {
			if entry.config == cfg {
				_ = entry.worker.SubmitCommand(discharger.ControlCommand{
					Type: discharger.CommandUpdateConfig,
					Config: &discharger.ConfigUpdate{
						Schedules: scheduleByBattery[name],
					},
				})
				continue
			}
			if removed, ok := m.remove(name); ok {
				log.With(slog.String("battery", name)).Info("restarting discharge worker (config changed)")
				go removed.worker.Stop()
			}
		}

		entry, err := startWorker(ctx, wg, cfg, scheduleByBattery[name], log)
		if err != nil {
			log.With(slog.String("battery", name), sl.Err(err)).Error("starting discharge worker")
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

func startWorker(ctx context.Context, wg *sync.WaitGroup, battery entity.BatteryConfig, schedules []entity.Schedule, log *slog.Logger) (*workerEntry, error) {
	workerLog := log.With(slog.String("battery", battery.Name))
	api := apiclient.New(battery.Url, battery.Token, workerLog)

	worker, err := discharger.New(battery.Name, api, workerLog)
	if err != nil {
		return nil, fmt.Errorf("creating discharge worker: %w", err)
	}

	for _, schedule := range schedules {
		worker.AddSchedule(schedule)
	}

	worker.SetCapacityLimit(battery.CapacityLimit)

	wg.Add(1)
	go func() {
		defer wg.Done()

		stopCh := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				worker.Stop()
			case <-stopCh:
			}
		}()

		if err := worker.Run(); err != nil {
			workerLog.Error("running discharge worker", sl.Err(err))
		}
		workerLog.Info("discharge worker stopped")
		close(stopCh)
	}()

	return &workerEntry{
		worker: worker,
		config: battery,
	}, nil
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
	default:
		return discharger.ControlCommand{}, fmt.Errorf("unsupported remote command: %s", cmd.Command)
	}
}
