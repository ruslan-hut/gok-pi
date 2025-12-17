# GOK-Pi - Battery Management Automation Service

## Project Overview

Battery management automation service for Sonnen battery systems. Provides automated discharge/charge scheduling, remote monitoring via WebSocket, and Prometheus metrics.

## Tech Stack

- **Backend**: Go 1.24.0
- **Frontend**: React 18.3 + TypeScript + Vite
- **Key Libraries**: gorilla/websocket, nhooyr.io/websocket, cleanenv, prometheus/client_golang
- **Logging**: log/slog (structured logging)

## Project Structure

```
cmd/
  gok/           # Agent binary (runs on Raspberry Pi)
  controlserver/ # Central control server
  agentupdater/  # Binary auto-updater

battery/
  api-client/    # HTTP client for Sonnen API
  discharger/    # Discharge scheduling/control
  charger/       # Charge scheduling/control
  entity/        # Data structures

internal/
  config/        # Configuration management (YAML + env vars)
  remote/wsclient/ # WebSocket client for agents
  lib/           # Utilities (logger, sl, timer)

metrics/
  observers/     # Prometheus metrics collection
  server/        # /metrics HTTP endpoint

remote/server/   # Control server WebSocket + config store

web/ui/          # React dashboard (Vite build)

deploy/          # systemd units, nginx config, build scripts
```

## Common Commands

### Build

```bash
# Agent (local)
go build -o gok ./cmd/gok

# Control server
go build -o controlserver ./cmd/controlserver

# Cross-compile for Raspberry Pi
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o gok ./cmd/gok
```

### Run

```bash
# Agent
./gok -conf config.yml -log .

# Control server
./controlserver -addr :8080 -secret "shared-secret" -static ./web/ui/dist
```

### Test

```bash
go test ./...           # All tests
go test -v ./...        # Verbose
go test -cover ./...    # With coverage
```

### React UI

```bash
cd web/ui
npm install
npm run dev      # Development (localhost:5173)
npm run build    # Production build
```

## Configuration

- `config.yml` - Main configuration
- `config-local.yml` - Local overrides (git-ignored)

Key sections: `device_name`, `batteries[]`, `schedules[]`, `charge_schedules[]`, `metrics`, `remote_control`

## Key Patterns

- **Worker Pattern**: Discharger/Charger use command channels for control
- **Concurrency**: RWMutex for config, buffered channels for commands, context for cancellation
- **Error Handling**: `fmt.Errorf()` with `%w` wrapping
- **Logging**: slog with `sl.Err()`, `sl.Secret()`, `sl.Module()` helpers
- **API Retry**: Exponential backoff (5 attempts, 3s step)

## Naming Conventions

- Receivers: Single-letter (`c`, `d`)
- Exported: PascalCase
- Unexported: camelCase
- Log attributes: snake_case

## Deployment

- GitHub Actions: `.github/workflows/deploy.yml`
- systemd units in `deploy/`
- Cross-compile: linux/arm64 (agent), linux/amd64 (control server)
