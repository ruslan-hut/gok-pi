// Control Server is the central monitoring and command hub for GOK-Pi agents.
//
// It accepts WebSocket connections from agents and the React UI, aggregates
// telemetry, persists agent configs, fetches electricity prices, generates
// auto-schedules, and tracks charge/discharge energy sessions.
//
// Usage:
//
//	go run ./cmd/controlserver -addr :8080 -secret "<shared-secret>" -static ./web/ui/dist
package main

import (
	"flag"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/ilyakaznacheev/cleanenv"

	"gok-pi/remote/server"
	"gok-pi/remote/server/email"
)

type CSConfig struct {
	Addr          string               `yaml:"addr" env:"CONTROL_ADDR" env-default:":8080"`
	Secret        string               `yaml:"secret" env:"CONTROL_SECRET" env-default:""`
	Static        string               `yaml:"static" env:"CONTROL_STATIC" env-default:""`
	AgentBinary   string               `yaml:"agent_binary" env:"GOK_CONTROL_AGENT_BINARY" env-default:""`
	VersionFile   string               `yaml:"version_file" env:"GOK_CONTROL_VERSION_FILE" env-default:"VERSION"`
	ConfigStore   string               `yaml:"config_store" env-default:"data/agent-configs.json"`
	SessionDB     string               `yaml:"session_db" env-default:"data/sessions.db"`
	UIUsername    string               `yaml:"ui_username" env:"GOK_UI_USERNAME" env-default:""`
	UIPassword    string               `yaml:"ui_password" env:"GOK_UI_PASSWORD" env-default:""`
	LogFile       string               `yaml:"log_file" env:"GOK_CS_LOG_FILE" env-default:""`
	EmailProvider email.ProviderConfig `yaml:"email_provider"`
	EmailState    string               `yaml:"email_state" env-default:"data/email-reports-state.json"`
}

func main() {
	configPath := flag.String("config", "", "path to YAML config file")
	flag.Parse()

	var cfg CSConfig
	if *configPath != "" {
		if err := cleanenv.ReadConfig(*configPath, &cfg); err != nil {
			slog.Error("failed to load config file", slog.Any("error", err))
			os.Exit(1)
		}
	} else {
		if err := cleanenv.ReadEnv(&cfg); err != nil {
			slog.Error("failed to read env config", slog.Any("error", err))
			os.Exit(1)
		}
	}

	var logWriter io.Writer = os.Stdout
	if cfg.LogFile != "" {
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			slog.Error("failed to open log file", slog.Any("error", err))
			os.Exit(1)
		}
		defer f.Close()
		logWriter = f
	}

	logger := slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	srv := server.New(server.Config{
		SharedSecret:  strings.TrimSpace(cfg.Secret),
		UIStaticDir:   strings.TrimSpace(cfg.Static),
		AgentBinary:   strings.TrimSpace(cfg.AgentBinary),
		VersionFile:   strings.TrimSpace(cfg.VersionFile),
		ConfigStore:   strings.TrimSpace(cfg.ConfigStore),
		SessionDB:     strings.TrimSpace(cfg.SessionDB),
		UIUsername:    strings.TrimSpace(cfg.UIUsername),
		UIPassword:    strings.TrimSpace(cfg.UIPassword),
		EmailProvider: cfg.EmailProvider,
		EmailState:    strings.TrimSpace(cfg.EmailState),
	}, logger)

	logger.Info("starting control server", slog.String("addr", cfg.Addr))

	if err := srv.ListenAndServe(cfg.Addr); err != nil {
		logger.Error("control server exited", slog.Any("error", err))
		os.Exit(1)
	}
}
