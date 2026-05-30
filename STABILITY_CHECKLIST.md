# Stability & Correctness Checklist

Tracking remediation from the full-project analysis. Severity: HIGH / MED / LOW.
Status: `[ ]` todo · `[~]` in progress · `[x]` done.

## Tier 1 — correctness of battery actuation / data integrity ✅ DONE (branch: stability-tier1)

- [x] **A-HIGH** Coordinate operating-mode ownership between charge & discharge controllers.
  New `battery/controller/mode.go` (`ModeCoordinator`); switch-to-auto only when neither
  direction active. Wired via `SetModeCoordinator` + shared instance in `startWorker`.
- [x] **A-MED** Honor SoC limit on manual start; clamp rate non-negative.
  `controller.go` `CommandStart` + `calculateRate`.
  NOTE: upper power clamp deferred — no hardware-max field in config; belongs in
  `Schedule.Validate` (tracked as tier-3 C-LOW).
- [x] **E-HIGH** Fix `config.Save()` slice-aliasing data race (deep-copy slices under lock).
  `internal/config/config.go` Save() now deep-copies Batteries/Schedules/ChargeSchedules.
- [x] **E-MED** Serialize `Save()` writers via dedicated `saveMu`. `internal/config/config.go`.
- [x] **E-MED** Add fsync to atomic file write (temp Sync + dir fsync). `internal/lib/atomicfile/atomicfile.go`.
- [x] **A-HIGH** Re-derive manual-override on startup so restart doesn't cancel a manual op.
  `syncStateFromBattery` + `hasActiveSchedule`; also `MarkActive` on coordinator.
- [x] **C-HIGH(latent)** Deep-copy `GoalReachedTime` pointer in `CloneSchedules`. `battery/entity/clone.go`.

Verify before merge: `go build ./... && go test -race ./internal/config/... ./battery/... ./cmd/gok/...`

## Tier 2 — availability / not-silently-broken (branch: stability-tier1, in progress)

- [x] **F-HIGH** Reset reconnect backoff once a connection is established. `wsclient/client.go` (`onConnected` resets backoff in `connectAndServe`).
- [x] **F/I-HIGH** WS read deadlines + ping/pong on agent & UI sides; per-write deadlines.
  `agent.go` (pong handler, ping ticker, writeWait), `ui.go` (same), `wsclient/client.go` (`writeJSON` with timeout).
  Constants `pongWait=70s, pingPeriod=30s, writeWait=10s`.
- [x] **H-HIGH** Price-data freshness validation: reject partial (<20h) and wrong-date days;
  day-rollover promotes Tomorrow→Today; drop stale Today. `redata`/`pricefetcher/fetcher.go`.
- [x] **H-HIGH** Import `time/tzdata` in both `cmd/gok` and `cmd/controlserver` to stop summer 1h shift.
- [x] **H-HIGH** Fix hour-boundary representation: `formatHour` emits `00:00`/`24:00`; `24:00` end-of-day
  sentinel honored by `entity.Schedule.Validate` (`isValidScheduleTime`) and `timer.ParseTimeInLocation`.
- [x] **I-HIGH** Authenticate `/api/ui` (`requireAuthWS`) and `/api/agents*` reads (`requireAuth`);
  constant-time compare for shared secret + login. Frontend now sends token on agents/config/logs GETs
  and `?token=` on the UI WS (`api.ts`, `useAgents.ts`). Fail-open preserved when no creds configured.
- [x] **H-MED** Charge/discharge overlap dedup on flat price days. `electricity/scheduler/scheduler.go`.
- [ ] **A-MED** Decouple `Status()` polling from control loop; single poller per battery. `controller.go:258`, `main.go:650`. DEFERRED (larger refactor).
- [ ] **H-MED** Fallback `GenerateSchedules` uses Madrid time not server-local. `generator.go:146,161`. DEFERRED (verify caller still uses fallback path first).
- [ ] **B-MED** redata: tolerant datetime parsing (Z / no-millis / no-colon offset). `redata/client.go:106`. DEFERRED.

Verify: `go build ./... && go test ./...` (all green); `cd web/ui && npx tsc --noEmit` (green).

## Tier 3 — hardening (branch: stability-tier1)

- [x] **J-MED** Metrics listeners copied under RLock, invoked outside it. `metrics/observers/listeners.go` (`notifyListeners`).
- [x] **J-MED** Metrics HTTP server uses configured `http.Server` with Read/Write/Idle/ReadHeader timeouts. `metrics/server/server.go`.
- [x] **K-MED** Updater: ERROR-level log on restart failure (stale-code drift); `tempFile.Sync()` after download before rename. `cmd/agentupdater/updater.go`.
- [x] **I-MED** Server graceful shutdown: cancellable ctx for background goroutines, SIGINT/SIGTERM → `srv.Shutdown` + `sessions.Store().Close()`; `ErrServerClosed` treated as clean. `remote/server/server.go`.
- [x] **I-MED** `getAvgPrice` walks the window hour-by-hour by timestamp (correct across midnight). `remote/server/sessions.go`.
- [x] **I-MED** Schedule migration: `sql.ErrNoRows` → drop+recreate; any other probe error surfaced; DROP error captured. `sessiondb/schedules.go`.
- [x] **B-MED** Driver: typed `httpStatusError`; 4xx (except 429) not retried; no sleep after final attempt. `driver/sonnen/sonnen.go`.
- [x] **C-LOW** Schedule.Validate rejects start==stop (zero-length window). `entity/schedule-config.go`.
- [ ] **I-MED** Session DB writes off `st.mu` (async persist queue). DEFERRED — needs a persistence queue; correctness-sensitive, own change.
- [ ] **A-LOW** Remove dead controller fields/docs (`capacity`, `capacityLimit`, `stopTime`, `SetCapacityLimit`). DEFERRED — cosmetic; avoid churn in the verified controller hot path.
- [ ] **C-LOW** Schedule.Validate upper power bound. DEFERRED — no hardware-max field in config to clamp against.

Verify: `go build ./...` clean; `go test ./...` all green (6 pkgs with tests); `go test -race ./remote/server/ ./battery/... ./metrics/... ./cmd/agentupdater/ ./internal/...` race-clean; `gofmt -l`/`go vet` clean.
