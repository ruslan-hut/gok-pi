package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"gok-pi/battery/api-client"
	"gok-pi/battery/discharger"
	"gok-pi/battery/entity"
	"gok-pi/internal/config"
	"gok-pi/internal/lib/logger"
	"gok-pi/internal/lib/sl"
	"gok-pi/internal/remote/wsclient"
	"gok-pi/metrics/server"
	"log/slog"
	"strings"
	"sync"
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
		lg.Warn("no batteries enabled")
		return
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

	var wg sync.WaitGroup
	workers := make(map[string]*discharger.Discharge)

	for _, batteryCfg := range batteries {
		batteryCfg := batteryCfg
		log := lg.With(slog.String("battery", batteryCfg.Name))
		api := apiclient.New(batteryCfg.Url, batteryCfg.Token, log)

		worker, err := discharger.New(batteryCfg.Name, api, log)
		if err != nil {
			log.Error("creating discharge worker", sl.Err(err))
			continue
		}

		for _, s := range schedules {
			if s.BatteryName == batteryCfg.Name {
				worker.AddSchedule(s)
			}
		}

		worker.SetCapacityLimit(batteryCfg.CapacityLimit)
		workers[batteryCfg.Name] = worker

		wg.Add(1)
		go func(worker *discharger.Discharge, workerLog *slog.Logger) {
			defer wg.Done()
			if err := worker.Run(); err != nil {
				workerLog.Error("running discharge worker", sl.Err(err))
			}
			workerLog.Info("discharge worker stopped")
		}(worker, log)
	}

	if conf.RemoteControl.Enabled {
		lg.Info("starting remote control client", slog.String("url", conf.RemoteControl.ServerURL))
		remoteClient := wsclient.New(conf.RemoteControl, wsclient.AgentMetadata{
			ID:  conf.Env,
			Env: conf.Env,
		}, lg)
		remoteClient.Run(ctx)
		go handleRemoteCommands(ctx, remoteClient.Commands(), workers, lg)
	}

	wg.Wait()

	lg.Info("gok-pi stopped")
}

func handleRemoteCommands(ctx context.Context, commands <-chan wsclient.Command, workers map[string]*discharger.Discharge, log *slog.Logger) {
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
			worker, ok := workers[cmd.Target]
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
