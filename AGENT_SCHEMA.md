# Agent Logic Schema

## Overview

**gok-pi** is a battery management and control agent written in Go. It implements a worker-based pattern to manage battery charging/discharging operations through scheduled automation and remote control.

---

## Package Structure

```
gok-pi/
├── cmd/
│   ├── gok/              # Main agent entry point
│   ├── controlserver/    # Remote control server
│   └── agentupdater/     # Auto-update utility
├── battery/
│   ├── controller/       # Unified charge/discharge control loop
│   ├── driver/           # Battery driver interface and vendor implementations
│   └── entity/           # Shared domain types and helpers
├── internal/
│   ├── config/           # Configuration management
│   ├── lib/
│   │   ├── atomicfile/   # Atomic file write utility
│   │   ├── logger/       # Structured logging
│   │   ├── timer/        # Time utilities
│   │   └── sl/           # Slog helpers
│   └── remote/
│       └── wsclient/     # WebSocket client
├── metrics/
│   ├── observers/        # Prometheus & telemetry
│   └── server/           # Metrics HTTP server
└── remote/
    └── server/           # Remote control server
```

---

## Package Descriptions

### cmd/gok
**Purpose:** Main agent entry point - bootstraps and orchestrates the entire agent lifecycle.

| Function | Description |
|----------|-------------|
| `main()` | Entry point; initializes config, sets up workers and remote control |
| `startWorker()` | Creates discharge/charge controller workers for each battery |
| `monitorBattery()` | Continuous 10-second polling of battery status |
| `handleRemoteCommands()` | Routes commands to appropriate workers |
| `workerManager.Apply()` | Manages worker lifecycle (create, update, delete) |

### cmd/controlserver
**Purpose:** Remote control server that receives agent connections and manages bidirectional communication.

### cmd/agentupdater
**Purpose:** Auto-update utility for agent binary updates.

---

### battery/driver (+ battery/driver/sonnen)
**Purpose:** Battery driver interface and Sonnen REST API implementation.

The `driver.Driver` interface defines operations that all battery vendors must implement. Drivers self-register via `init()` and are resolved by name at runtime.

| Function | Description |
|----------|-------------|
| `Status()` | GET /status - fetch battery state |
| `StartCharge(power)` | POST /setpoint/charge/{power} - start charging |
| `StopCharge()` | POST /setpoint/charge/0 - stop charging |
| `StartDischarge(power)` | POST /setpoint/discharge/{power} - start discharging |
| `StopDischarge()` | POST /setpoint/discharge/0 - stop discharging |
| `SwitchOperatingModeToManual()` | PUT /configurations?EM_OperatingMode=1 |
| `SwitchOperatingModeToAuto()` | PUT /configurations?EM_OperatingMode=2 |

**Retry Strategy:** Max 5 retries, backoff 3s × attempt number, 5s request timeout.

---

### battery/controller
**Purpose:** Unified charge/discharge control loop parameterized by `Direction`.

| Function | Description |
|----------|-------------|
| `New()` | Create new controller with a given Direction (charge or discharge) |
| `Run()` | Main worker loop (10s ticker) - process commands, check status, evaluate schedules |
| `Stop()` | Graceful shutdown via stop channel |
| `SubmitCommand()` | Queue control command for async processing |
| `checkTime()` | Evaluate if current time is within active schedule for this direction |
| `runOperation()` | Execute operation (switch mode, start charge/discharge) |
| `stopOperation()` | Stop operation and return to auto mode |
| `processControlCommand()` | Handle remote commands (start/stop/set limits/force mode/update config/reset goal) |
| `DischargeDirection()` | Returns Direction configured for discharge (SoC <= limit stops) |
| `ChargeDirection()` | Returns Direction configured for charge (SoC >= limit stops) |

**Control Commands:** `CommandStart`, `CommandStop`, `CommandSetLimits`, `CommandForceMode`, `CommandUpdateConfig`, `CommandResetGoal`

---

### battery/entity
**Purpose:** Shared domain types and helpers for battery configuration and status.

| Type/Function | Description |
|---------------|-------------|
| `BatteryConfig` | Hardware config: name, URL, token, enabled, capacity/power/SoC limits |
| `Schedule` | Time-based schedule: name, type (charge/discharge), start/stop times, limits, run_once, goal_reached_time |
| `AgentConfig` | Agent configuration exchanged over the WebSocket protocol |
| `SystemStatus` | Real-time telemetry: RSOC, USOC, capacity, consumption, power, modes |
| `CloneSchedules()` | Shallow-copy a schedule slice |
| `CloneBatteryConfigs()` | Shallow-copy a battery config slice |
| `IsAutoSchedule()` | Check if a schedule name has the "auto-" prefix |

**Schedule.GoalReachedTime:** For `run_once` schedules, tracks when the SoC goal was reached. The schedule is skipped until `stop_time` passes, then the goal is cleared for the next day.

---

### internal/config
**Purpose:** Thread-safe configuration management with YAML persistence.

| Function | Description |
|----------|-------------|
| `MustLoad()` | Load config from YAML file (singleton); migrates old goal state format |
| `UpdateScheduleGoalReached()` | Update Schedule.GoalReachedTime and persist to config file |
| `ClearScheduleGoalReached()` | Clear Schedule.GoalReachedTime and persist to config file |
| `UpdateFromRemoteConfig()` | Merge remote config changes; preserves goal state for existing schedules |
| `Save()` | Atomic write (temp file + rename) |

---

### internal/remote/wsclient
**Purpose:** WebSocket client for remote control server communication.

| Function | Description |
|----------|-------------|
| `New()` | Create WebSocket client |
| `Run()` | Connect and maintain connection with auto-reconnect |
| `readLoop()` | Parse incoming messages, dispatch to handlers |
| `writeLoop()` | Send heartbeats, telemetry, config |
| `sendTelemetry()` | Push battery snapshots to server |

**Message Types:**
- Outbound: `agent.hello`, `agent.heartbeat`, `agent.telemetry`, `agent.config`
- Inbound: `server.config.push`, `agent.command`, `server.log.request`

---

### metrics/observers
**Purpose:** Publish battery telemetry for monitoring (Prometheus + remote).

| Function | Description |
|----------|-------------|
| `UpdateSoC()` | Update relative state of charge |
| `UpdateUSoC()` | Update user state of charge |
| `UpdateCapacity()` | Update remaining capacity |
| `UpdateConsumption()` | Update house consumption |
| `UpdatePac()` | Update AC power |
| `UpdateChargeState()` | Update charging flag |
| `UpdateDischargeState()` | Update discharging flag |
| `UpdateStatus()` | Update connection status |
| `RegisterListener()` | Add telemetry listener |
| `NotifyListeners()` | Push snapshots to all listeners |

---

### metrics/server
**Purpose:** HTTP server for Prometheus metrics scraping.

| Function | Description |
|----------|-------------|
| `Start()` | Start metrics HTTP server on configured port |

**Prometheus Metrics:** `battery_RSoC`, `battery_USoC`, `battery_RemainingCapacity_W`, `battery_Consumption_W`, `battery_Pac_total_W`, `battery_BatteryDischarging`, `battery_BatteryCharging`, `battery_BatteryOperatingMode`

---

### remote/server
**Purpose:** Accept agent WebSocket connections and manage bidirectional communication.

| Function | Description |
|----------|-------------|
| `handleAgentConnection()` | Accept and register agent WebSocket |
| `readLoop()` | Process incoming agent messages |
| `writeLoop()` | Send commands and config to agents |
| `SendCommand()` | Push command to specific agent |
| `BroadcastConfig()` | Push config update to all agents |

---

## Data Flow

```
┌─────────────────────────────────────────────────────────────┐
│                    CONFIGURATION                            │
│  YAML File → Config Singleton → WSClient (remote sync)     │
└─────────────────────────┬───────────────────────────────────┘
                          ↓
┌─────────────────────────────────────────────────────────────┐
│                   WORKER MANAGER                            │
│  Apply(batteries, schedules) → Create/Update/Remove Workers │
└─────────────────────────┬───────────────────────────────────┘
                          ↓
┌─────────────────────────────────────────────────────────────┐
│                   WORKER LAYER                              │
│  Monitor (10s) → API.Status() → Observers.Update()         │
│  Charge Controller (10s) → Check schedules → StartCharge/StopCharge     │
│  Discharge Controller (10s) → Check schedules → StartDischarge/StopDischarge │
└─────────────────────────┬───────────────────────────────────┘
                          ↓
┌─────────────────────────────────────────────────────────────┐
│                   TELEMETRY                                 │
│  Observers → Prometheus Metrics + WSClient → Remote Server │
└─────────────────────────┬───────────────────────────────────┘
                          ↓
┌─────────────────────────────────────────────────────────────┐
│                   REMOTE CONTROL                            │
│  Server ←→ WSClient (commands, config push, telemetry)     │
└─────────────────────────────────────────────────────────────┘
```

---

## Key Algorithms

### Schedule Time Evaluation
1. Parse start/stop times in configured timezone
2. Handle midnight-spanning schedules (22:00 → 06:00)
3. For `run_once`: check `Schedule.GoalReachedTime` - if set and `stop_time` not passed, skip schedule
4. When `stop_time` passes after goal reached, clear `GoalReachedTime` (allows re-run next day)
5. Validate conditions: SoC within limits, power limit > 0

### Goal Tracking (run_once schedules)
1. Goal state stored in `Schedule.GoalReachedTime` field (embedded in schedule)
2. When SoC limit reached, record `GoalReachedTime = now` and persist via callback
3. In `checkTime()`, skip schedule if goal reached and current time < stop_time on goal day
4. After stop_time passes, clear `GoalReachedTime` to allow re-run
5. Automatic cleanup: deleted schedules take their goal state with them

### Worker Lifecycle Management
1. Compare desired vs current battery configs
2. If URL/token changed: restart workers (new API client)
3. If only schedules changed: send `CommandUpdateConfig`
4. If new battery: create charge + discharge controller workers

### Graceful Shutdown
1. Context cancellation propagates to all components
2. Workers stop via `Stop()` channel
3. API connections close
4. Remote client disconnects
5. WaitGroup ensures all goroutines complete

---

## Concurrency Patterns

| Pattern | Usage |
|---------|-------|
| RWMutex | Worker manager, config, observers |
| Channels | Worker commands (16 buffer), telemetry (64), remote messages |
| Context | Cancellation propagation for graceful shutdown |
| WaitGroup | Track worker goroutine completion |

---

## Error Handling

| Scenario | Strategy |
|----------|----------|
| API failures | Retry up to 5 times with exponential backoff |
| Status unavailable | Mark battery as "Disconnected" |
| Remote disconnect | Auto-reconnect with backoff (5s → 60s) |
| Command queue full | Drop oldest, enqueue new |
| Config parse errors | Log and skip invalid entries |
