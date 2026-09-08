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
go test ./battery/controller/...        # Specific package
go test -run TestName ./package/...     # Single test
```

## Architecture

Three main executables:
- **Agent** (`cmd/gok`) - Runs on device near battery, polls Sonnen API every 10 seconds, executes discharge/charge schedules
- **Control Server** (`cmd/controlserver`) - Central WebSocket hub for multi-agent telemetry and remote commands
- **Agent Updater** (`cmd/agentupdater`) - Auto-updates agent binary by comparing SHA-256 hashes

Key packages:
- `battery/controller` - Unified charge/discharge control loop, parameterized by direction
- `battery/driver` - Battery driver interface and registry; drivers self-register via `init()`
- `battery/driver/sonnen` - Sonnen battery API driver implementation
- `battery/driver/huawei` - Huawei LUNA2000B point table, codec and alarms; **no driver yet**, see `HUAWEI_INTEGRATION.md`
- `battery/entity` - Shared domain types (BatteryConfig, Schedule, AgentConfig, SystemStatus) and helpers
- `internal/modbus` - Modbus-TCP client, read-only by construction (only `0x03` and `0x2B`); `modbussim` is its test server
- `internal/remote/wsclient` - Agent-side WebSocket client with reconnection backoff
- `remote/server` - Control server WebSocket handlers, config store, UI serving
- `remote/server/email` - Brevo-backed daily/weekly/monthly email reports built from session DB + price fetcher
- `remote/server/chargers.go` + `charger_links.go` - evsys EV charger integration: webhook receiver, charger↔battery links, active session tracking
- `metrics/observers` - Prometheus gauges and telemetry snapshots
- `internal/config` - YAML config loading via cleanenv, thread-safe updates
- `internal/lib/atomicfile` - Atomic file writes (write-to-temp-then-rename)

## Configuration

Config file: `config.yml` (YAML format)
- `batteries[]` - Battery endpoints with driver, URL, token, capacity_limit
- `schedules[]` - Time-based discharge/charge windows with power_limit and soc_limit
- `remote_control` - WebSocket connection to control server (enabled, server_url, shared_secret)
- `metrics` - Prometheus endpoint settings

Control server config (`cmd/controlserver`) additionally has `charger_token` /
`charger_links` / `charger_sessions` for the evsys integration; see
`EVSYS_INTEGRATION.md`.

Environment variables override config values via cleanenv tags.

## Data Flow

1. Agent loads `config.yml`, starts workers for enabled batteries/schedules
2. Workers poll Sonnen API, execute scheduled operations based on time windows
3. If remote_control enabled: WebSocket streams telemetry to control server, receives commands
4. Control server aggregates telemetry, broadcasts to web UI clients, routes commands to agents
5. Config updates from UI persist to agent's local `config.yml` via optimistic locking
6. EV charger integration: evsys POSTs `transaction.start`/`transaction.stop` to `/api/webhooks/evsys`; the server matches the event to a charger link (by evsys location, falling back to charge point id) and sends `start_discharge`/`stop_discharge` to that link's agent. The discharge is an override that outranks schedules and survives config pushes; see `EVSYS_INTEGRATION.md`.
7. Email scheduler (control server) ticks every minute; for each agent with `email_reports.enabled` and a populated recipients list, it dispatches daily / weekly (Mon) / monthly (1st) summaries through Brevo at the agent's configured `send_hour` in the agent timezone. A report is built when at least one recipient is subscribed to it; subscriptions are the only switch (the old agent-level `daily`/`weekly`/`monthly` flags are kept for migration only). Last-sent date per (agent, kind) is persisted to `data/email-reports-state.json` so reports are not duplicated across restarts. Brevo credentials live in the controlserver config (`email_provider`); per-agent recipients/toggles live in `AgentConfig.email_reports` and are editable from the web UI.

8. Connectivity alerts (control server): `agentWatcher` (`remote/server/agent_alerts.go`) raises one email per outage when an agent stays disconnected for 10 minutes, and one recovery notice when it returns. Agents known to the config store are seeded as offline at startup, so a device that is down while the server restarts is still reported. Alerts need `email_reports.enabled` plus recipients subscribed to `alerts`.
   Subscriptions are per address: `email_reports.recipients` is a list of `{address, daily, weekly, monthly, alerts}`. `EmailReportsConfig.UnmarshalJSON` still accepts the original plain-string list — migrated addresses inherit the agent's report toggles and are never auto-subscribed to alerts. Use `RecipientsFor(entity.EmailKind*)` rather than reading `Recipients` directly.
9. Remote restart: `POST /api/agents/<id>/restart` sends the `restart_agent` command; the agent shuts down as it would on SIGTERM and exits 75 (`restartExitCode`), which brings it back under either `Restart=always` or `Restart=on-failure`. Use it when an agent is reachable but misbehaving — the binary updater (30 min timer) is the path for shipping code.

## Battery Drivers

The agent uses a driver-based architecture (`battery/driver`) to support multiple battery vendors.
Each driver implements the `driver.Driver` interface and self-registers via `init()`.

Adding a new driver:
1. Create package `battery/driver/<name>/` implementing `driver.Driver`
2. Call `driver.Register("<name>", constructor)` in its `init()`
3. Add blank import `_ "gok-pi/battery/driver/<name>"` in `cmd/gok/main.go`
4. Add corresponding `<option>` in the Web UI driver select (`web/ui/src/components/config/BatteryConfigForm.tsx`)

The driver options in the UI must match the registered driver names in the backend.

`battery/driver/huawei` is a partial exception: it carries the LUNA2000B point
table, codec and alarm definitions but implements no `driver.Driver` and calls no
`driver.Register`, so it is data plus `cmd/essprobe`, not a working driver.
See `HUAWEI_INTEGRATION.md` for the site details, the SmartLogger change it is
blocked on, and the bring-up stages.

## Development Notes

- Always run `go build ./...` after making changes to verify the code compiles before reporting completion
