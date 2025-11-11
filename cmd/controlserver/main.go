package main

import (
	"flag"
	"log/slog"
	"os"

	"gok-pi/remote/server"
)

func main() {
	addr := flag.String("addr", ":8080", "address to bind the control server")
	sharedSecret := flag.String("secret", "", "shared secret required from gok-pi agents")
	staticDir := flag.String("static", "", "path to serve pre-built React UI assets")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	srv := server.New(server.Config{
		SharedSecret: *sharedSecret,
		UIStaticDir:  *staticDir,
	}, logger)

	if err := srv.ListenAndServe(*addr); err != nil {
		logger.Error("control server exited", slog.Any("error", err))
		os.Exit(1)
	}
}
