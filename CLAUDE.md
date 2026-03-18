# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

GOK-Pi is a Go-based service for automated battery discharge/charge control with optional remote monitoring and control. It uses a driver-based architecture to support multiple battery vendors (currently Sonnen). It manages battery systems via vendor APIs, executes scheduled operations, and exposes Prometheus metrics.

## Build and Run Commands

### Agent (primary battery controller)
```bash
go run ./cmd/gok -conf config.yml -log /var/log
go build -o gok ./cmd/gok
```

### Control Server (central monitoring)
```bash
go run ./cmd/controlserver -addr :8080 -secret "<shared-secret>" -static ./web/ui/dist
go build -o controlserver ./cmd/controlserver
```

### Agent Updater
```bash
go build -o agentupdater ./cmd/agentupdater
```

### Web UI (React dashboard)
```bash
cd web/ui
npm install
npm run dev      # Development (localhost:5173, proxies to Go server at :8080)
npm run build    # Production build to dist/
```

### Cross-compile agent for Raspberry Pi (ARM64)
```bash
./deploy/build_agent_release.sh -o /tmp/release --goos linux --goarch arm64
```

### Run Tests
```bash
go test ./...                           # All tests
go test ./battery/discharger/...        # Specific package
go test -run TestName ./package/...     # Single test
```

## Architecture

Three main executables:
- **Agent** (`cmd/gok`) - Runs on device near battery, polls Sonnen API every 10 seconds, executes discharge/charge schedules
- **Control Server** (`cmd/controlserver`) - Central WebSocket hub for multi-agent telemetry and remote commands
- **Agent Updater** (`cmd/agentupdater`) - Auto-updates agent binary by comparing SHA-256 hashes

Key packages:
- `battery/driver` - Battery driver interface and registry; drivers self-register via `init()`
- `battery/driver/sonnen` - Sonnen battery API driver implementation
- `battery/discharger` - Discharge control with schedule windows and power/SoC limits
- `battery/charger` - Charge control logic
- `internal/remote/wsclient` - Agent-side WebSocket client with reconnection backoff
- `remote/server` - Control server WebSocket handlers, config store, UI serving
- `metrics/observers` - Prometheus gauges and telemetry snapshots
- `internal/config` - YAML config loading via cleanenv, thread-safe updates

## Configuration

Config file: `config.yml` (YAML format)
- `batteries[]` - Battery endpoints with driver, URL, token, capacity_limit
- `schedules[]` - Time-based discharge/charge windows with power_limit and soc_limit
- `remote_control` - WebSocket connection to control server (enabled, server_url, shared_secret)
- `metrics` - Prometheus endpoint settings

Environment variables override config values via cleanenv tags.

## Data Flow

1. Agent loads `config.yml`, starts workers for enabled batteries/schedules
2. Workers poll Sonnen API, execute scheduled operations based on time windows
3. If remote_control enabled: WebSocket streams telemetry to control server, receives commands
4. Control server aggregates telemetry, broadcasts to web UI clients, routes commands to agents
5. Config updates from UI persist to agent's local `config.yml` via optimistic locking

## Battery Drivers

The agent uses a driver-based architecture (`battery/driver`) to support multiple battery vendors.
Each driver implements the `driver.Driver` interface and self-registers via `init()`.

Adding a new driver:
1. Create package `battery/driver/<name>/` implementing `driver.Driver`
2. Call `driver.Register("<name>", constructor)` in its `init()`
3. Add blank import `_ "gok-pi/battery/driver/<name>"` in `cmd/gok/main.go`
4. Add corresponding `<option>` in the Web UI driver select (`web/ui/src/components/config/BatteryConfigForm.tsx`)

The driver options in the UI must match the registered driver names in the backend.

## Development Notes

- Do not run build checks (`go build`, `go test`) automatically after making changes - the user will verify manually
