# GOK-Pi

Automated battery charge/discharge control with remote monitoring.

An agent runs next to each battery (a Raspberry Pi is enough), polls it through a
vendor driver, and executes time-window schedules with power and SoC limits. A
central control server aggregates telemetry from all agents over WebSocket,
serves a React dashboard, generates schedules from Spanish PVPC electricity
prices, sends email reports and alerts, and can drive discharge from EV charger
sessions.

Supported batteries: **Sonnen** (driver). Huawei LUNA2000B is in bring-up
(read-only Modbus).

## Quick start

```bash
# agent
go run ./cmd/gok -conf config.yml -log /var/log

# control server
go run ./cmd/controlserver -config deploy/gok-cs.config.example

# web UI (dev server on :5173, proxies to :8080)
cd web/ui && npm install && npm run dev

# tests
go test ./...
```

## Documentation

See [doc/README.md](doc/README.md).

## License

MIT. See `LICENSE`.
