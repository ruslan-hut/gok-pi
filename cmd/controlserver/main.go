package main

import (
	"flag"
	"log/slog"
	"os"
	"strings"

	"gok-pi/remote/server"
)

const (
	envAgentBinary = "GOK_CONTROL_AGENT_BINARY"
	envVersionFile = "GOK_CONTROL_VERSION_FILE"
	envUIUsername  = "GOK_UI_USERNAME"
	envUIPassword  = "GOK_UI_PASSWORD"
)

func main() {
	addr := flag.String("addr", ":8080", "address to bind the control server")
	sharedSecret := flag.String("secret", "", "shared secret required from gok-pi agents")
	staticDir := flag.String("static", "", "path to serve pre-built React UI assets")
	agentBinary := flag.String("agent-binary", envOrDefault(envAgentBinary, ""), "filename of the agent binary inside the downloads directory")
	versionFile := flag.String("version-file", envOrDefault(envVersionFile, "VERSION"), "filename served under /downloads that contains the agent hash manifest")
	configStore := flag.String("config-store", "data/agent-configs.json", "path to persisted agent configuration store")
	sessionDB := flag.String("session-db", "data/sessions.db", "path to SQLite session database")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	srv := server.New(server.Config{
		SharedSecret: *sharedSecret,
		UIStaticDir:  *staticDir,
		AgentBinary:  strings.TrimSpace(*agentBinary),
		VersionFile:  strings.TrimSpace(*versionFile),
		ConfigStore:  strings.TrimSpace(*configStore),
		SessionDB:    strings.TrimSpace(*sessionDB),
		UIUsername:   envOrDefault(envUIUsername, ""),
		UIPassword:   envOrDefault(envUIPassword, ""),
	}, logger)

	if err := srv.ListenAndServe(*addr); err != nil {
		logger.Error("control server exited", slog.Any("error", err))
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}
