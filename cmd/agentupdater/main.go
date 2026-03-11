// Agent Updater is a standalone daemon that auto-updates the gok agent binary.
//
// It periodically fetches a VERSION file (containing a SHA-256 hash) from the
// control server, compares it to the local agent binary's hash, and if different,
// downloads the new binary, swaps it atomically, and optionally restarts the
// agent systemd service.
//
// Configuration is via environment variables (GOK_UPDATE_VERSION_URL, GOK_UPDATE_BINARY_URL,
// GOK_UPDATE_AGENT_PATH, GOK_UPDATE_SERVICE_NAME) or CLI flags.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"gok-pi/internal/lib/logger"
)

const (
	envVersionURL      = "GOK_UPDATE_VERSION_URL"
	envBinaryURL       = "GOK_UPDATE_BINARY_URL"
	envAgentRoot       = "GOK_UPDATE_AGENT_ROOT"
	envBinaryName      = "GOK_UPDATE_BINARY_NAME"
	envTimeout         = "GOK_UPDATE_TIMEOUT"
	envLogEnv          = "GOK_UPDATE_LOG_ENV"
	envLogDir          = "GOK_UPDATE_LOG_DIR"
	envRestartService  = "GOK_UPDATE_RESTART_SERVICE"
	envRestartEnabled  = "GOK_UPDATE_RESTART_ENABLED"
)

func main() {
	defaults := loadDefaults()

	versionURL := flag.String("version-url", defaults.VersionURL, "HTTP(S) URL pointing to the remote VERSION manifest (required if env not set)")
	binaryURL := flag.String("binary-url", defaults.BinaryURL, "HTTP(S) URL pointing to the remote binary; defaults to the VERSION directory plus binary name")
	agentRoot := flag.String("agent-root", defaults.AgentRoot, "Directory containing the local gok binary and VERSION manifest")
	binaryName := flag.String("binary-name", defaults.BinaryName, "Binary filename stored under the agent-root directory")
	timeout := flag.Duration("timeout", defaults.Timeout, "HTTP timeout for remote fetch operations")
	logEnv := flag.String("log-env", defaults.LogEnv, "Logger environment: local, dev, or prod")
	logDir := flag.String("log-dir", defaults.LogDir, "Directory for JSON log output when log-env is dev or prod")
	restartService := flag.String("restart-service", defaults.RestartService, "Systemd service name to restart after successful update (empty to disable)")
	restartEnabled := flag.Bool("restart-enabled", defaults.RestartEnabled, "Enable automatic restart of agent service after successful update")

	flag.Parse()

	log := logger.SetupLogger(*logEnv, *logDir)

	if *versionURL == "" {
		log.Error("version-url flag is required")
		flag.Usage()
		os.Exit(2)
	}

	cfg := Config{
		AgentRoot:      *agentRoot,
		BinaryName:     *binaryName,
		VersionURL:     *versionURL,
		BinaryURL:      *binaryURL,
		Timeout:        *timeout,
		RestartService: *restartService,
		RestartEnabled: *restartEnabled,
	}

	if err := Run(context.Background(), cfg, log); err != nil {
		log.Error("update failed", slog.Any("error", err))
		os.Exit(1)
	}

	log.Info("update check complete")
}

type defaults struct {
	VersionURL     string
	BinaryURL      string
	AgentRoot      string
	BinaryName     string
	Timeout        time.Duration
	LogEnv         string
	LogDir         string
	RestartService string
	RestartEnabled bool
}

func loadDefaults() defaults {
	timeout := 30 * time.Second
	if val := os.Getenv(envTimeout); val != "" {
		if parsed, err := time.ParseDuration(val); err == nil {
			timeout = parsed
		}
	}

	agentRoot := os.Getenv(envAgentRoot)
	if agentRoot == "" {
		agentRoot = "."
	}

	binaryName := os.Getenv(envBinaryName)
	if binaryName == "" {
		binaryName = "gok"
	}

	logEnv := os.Getenv(envLogEnv)
	if logEnv == "" {
		logEnv = "prod"
	}

	logDir := os.Getenv(envLogDir)
	if logDir == "" {
		logDir = "/var/log/gok"
	}

	restartService := os.Getenv(envRestartService)
	if restartService == "" {
		restartService = "agent.service"
	}

	restartEnabled := true
	if val := os.Getenv(envRestartEnabled); val != "" {
		restartEnabled = val == "true" || val == "1" || val == "yes"
	}

	return defaults{
		VersionURL:     os.Getenv(envVersionURL),
		BinaryURL:      os.Getenv(envBinaryURL),
		AgentRoot:      agentRoot,
		BinaryName:     binaryName,
		Timeout:        timeout,
		LogEnv:         logEnv,
		LogDir:         logDir,
		RestartService: restartService,
		RestartEnabled: restartEnabled,
	}
}
