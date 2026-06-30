package logger

import (
	"io"
	"log"
	"log/slog"
	"os"

	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	envLocal    = "local"
	envDev      = "dev"
	envProd     = "prod"
	logFileName = "gok-pi.log"
)

// Log rotation limits keep the on-disk log bounded so a long-running agent on a
// small SD card cannot fill the filesystem.
const (
	logMaxSizeMB  = 50 // rotate when the active file reaches this size
	logMaxBackups = 5  // keep this many rotated files
	logMaxAgeDays = 30 // delete rotated files older than this
)

func SetupLogger(env, path string) *slog.Logger {
	var logger *slog.Logger

	switch env {
	case envLocal:
		logger = slog.New(
			slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}),
		)
	case envDev:
		logger = slog.New(
			slog.NewTextHandler(rotatingWriter(path), &slog.HandlerOptions{Level: slog.LevelDebug}),
		)
	case envProd:
		logger = slog.New(
			slog.NewTextHandler(rotatingWriter(path), &slog.HandlerOptions{Level: slog.LevelInfo}),
		)
	default:
		log.Fatal("invalid environment: ", env)
	}

	return logger
}

// rotatingWriter returns a size-rotating writer for the agent log file. Rotation
// (size cap, backup retention, max age) bounds disk usage while preserving the
// append semantics earlier callers relied on; the file stays pull-on-demand via
// the control server's log request.
func rotatingWriter(path string) io.Writer {
	logPath := logFilePath(path)
	log.Printf("log file: %s", logPath)
	w := &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    logMaxSizeMB,
		MaxBackups: logMaxBackups,
		MaxAge:     logMaxAgeDays,
		Compress:   true,
	}
	// lumberjack opens lazily and slog discards writer errors, so an unwritable
	// log file (wrong owner/permissions after a restart) would silently freeze
	// the log while the process keeps running. Probe up front and fall back to
	// stderr so the failure is visible in the journal instead of disappearing.
	if _, err := w.Write(nil); err != nil {
		log.Printf("WARNING: cannot write log file %s: %v; falling back to stderr", logPath, err)
		return os.Stderr
	}
	return w
}

func logFilePath(path string) string {
	return path + "/" + logFileName
}
