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
	envVersionURL = "GOK_UPDATE_VERSION_URL"
	envBinaryURL  = "GOK_UPDATE_BINARY_URL"
	envAgentRoot  = "GOK_UPDATE_AGENT_ROOT"
	envBinaryName = "GOK_UPDATE_BINARY_NAME"
	envTimeout    = "GOK_UPDATE_TIMEOUT"
	envLogEnv     = "GOK_UPDATE_LOG_ENV"
	envLogDir     = "GOK_UPDATE_LOG_DIR"
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

	flag.Parse()

	log := logger.SetupLogger(*logEnv, *logDir)

	if *versionURL == "" {
		log.Error("version-url flag is required")
		flag.Usage()
		os.Exit(2)
	}

	cfg := Config{
		AgentRoot:  *agentRoot,
		BinaryName: *binaryName,
		VersionURL: *versionURL,
		BinaryURL:  *binaryURL,
		Timeout:    *timeout,
	}

	if err := Run(context.Background(), cfg, log); err != nil {
		log.Error("update failed", slog.Any("error", err))
		os.Exit(1)
	}

	log.Info("update check complete")
}

type defaults struct {
	VersionURL string
	BinaryURL  string
	AgentRoot  string
	BinaryName string
	Timeout    time.Duration
	LogEnv     string
	LogDir     string
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

	return defaults{
		VersionURL: os.Getenv(envVersionURL),
		BinaryURL:  os.Getenv(envBinaryURL),
		AgentRoot:  agentRoot,
		BinaryName: binaryName,
		Timeout:    timeout,
		LogEnv:     logEnv,
		LogDir:     logDir,
	}
}
