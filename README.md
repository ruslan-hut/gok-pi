# GOK-Pi
## Service to control battery discharging

The aim of this project is to develop a service that automates battery discharging control using the Sonnen controller's API. The service fulfills two main objectives:

1. **Automation**: It simplifies the process of battery management by automating various tasks, such as monitoring battery status, controlling charging and discharging based on set parameters, and more.

2. **Optimization**: It utilizes the power of automation and real-time data to optimize the battery usage and durability. This potentially leads to more efficient energy use, cost savings, and prolongs battery life.

## Project Purpose and Scope

With the increasing importance of sustainable energy, battery storage and management is a critical component of the power grid. This Sonnen Battery Controller turns Controller's API usage into a more automated and simplified task.

This service could be utilized in residential, commercial, or utility-scale settings where Sonnen battery systems are installed. Potential uses include renewable energy storage, backup power, load shifting, and more.

## Architecture Overview

The GOK-Pi system consists of three main executables and several core components that work together to provide automated battery management with optional remote monitoring and control.

### Executables

1. **Agent (`cmd/gok/main.go`)** - The primary battery control service that runs on each device (e.g., Raspberry Pi) near the battery installation. It:
   - Manages one or more battery systems through the Sonnen API
   - Executes scheduled discharge operations based on time windows and limits
   - Monitors battery status (SoC, capacity, consumption, etc.) every 10 seconds
   - Optionally connects to a remote control server for telemetry and remote commands
   - Exposes Prometheus metrics when enabled
   - Supports dynamic configuration updates via remote control or local config file

2. **Control Server (`cmd/controlserver/main.go`)** - Central server for remote monitoring and management. It:
   - Maintains WebSocket connections with multiple agents
   - Aggregates telemetry from all connected agents
   - Brokers commands from the web UI to agents
   - Manages per-agent configuration overrides with optimistic locking
   - Serves the React web dashboard
   - Hosts agent binaries and version manifests for auto-updates
   - Provides REST API endpoints for agent management

3. **Agent Updater (`cmd/agentupdater/main.go`)** - Standalone service for automatic agent updates. It:
   - Periodically checks for new agent binary versions
   - Compares remote SHA-256 hash with local version
   - Downloads and replaces the agent binary atomically
   - Optionally restarts the agent service after successful updates

### Core Components

#### Battery Management (`battery/`)

- **Controller (`battery/controller/controller.go`)** - Unified charge/discharge control logic:
  - Single parameterized controller handles both charge and discharge operations
  - Direction-specific behavior (schedule filter, SoC stop condition, API calls) injected via `Direction` struct
  - Implements scheduled time windows with configurable start/stop times
  - Enforces power and SoC limits per schedule
  - Supports manual override mode for remote commands
  - Manages operating mode transitions (auto/manual) with the battery controller
  - Processes control commands (start/stop, set limits, force mode, update config, reset goal)

- **Driver (`battery/driver/` + `battery/driver/sonnen/`)** - Battery driver interface and Sonnen HTTP client:
  - Retrieves battery status and system information
  - Controls discharge operations (start/stop with power settings)
  - Manages operating mode (auto/manual)
  - Implements retry logic with exponential backoff
  - Handles authentication via API tokens

- **Entities (`battery/entity/`)** - Shared domain types and helpers:
  - `BatteryConfig` - Configuration for individual battery systems
  - `Schedule` - Time-based charge/discharge schedules with power and SoC limits
  - `AgentConfig` - Agent configuration exchanged over the WebSocket protocol
  - `SystemStatus` - Real-time battery status from the API
  - `BatteryInfo` - Battery metadata and capabilities
  - `CloneSchedules()`, `CloneBatteryConfigs()` - Slice copy helpers
  - `IsAutoSchedule()` - Auto-generated schedule name detection

#### Remote Control (`internal/remote/wsclient/` and `remote/server/`)

- **WebSocket Client (`internal/remote/wsclient/client.go`)** - Agent-side remote control:
  - Establishes persistent WebSocket connection to control server
  - Streams telemetry snapshots at regular intervals
  - Receives and processes remote commands (discharge control, config updates)
  - Implements exponential backoff reconnection logic
  - Handles authentication via shared secret header
  - Publishes initial configuration snapshot on connection

- **WebSocket Server (`remote/server/server.go`)** - Control server-side:
  - Manages agent and UI WebSocket connections
  - Routes commands from UI to specific agents
  - Broadcasts telemetry updates to all connected UI clients
  - Handles agent registration and disconnection
  - Implements authentication for both agents and UI clients

- **Config Store (`remote/server/config_store.go`)** - Persistent configuration management:
  - Stores per-agent configuration overrides in JSON format
  - Implements optimistic locking via revision numbers
  - Seeds initial config from agent snapshots when missing
  - Provides thread-safe access to configuration data

#### Metrics (`metrics/`)

- **Observers (`metrics/observers/observers.go`)** - Metrics collection:
  - Exposes Prometheus gauges for battery metrics (SoC, capacity, consumption, etc.)
  - Maintains in-memory snapshots for telemetry streaming
  - Updates metrics atomically as battery status changes
  - Tracks battery connection status

- **Server (`metrics/server/server.go`)** - Metrics HTTP endpoint:
  - Serves Prometheus metrics at `/metrics`
  - Runs as optional background service when enabled in config

#### Configuration (`internal/config/`)

- **Config Manager (`internal/config/config.go`)** - Configuration management:
  - Loads YAML configuration files with environment variable overrides
  - Provides thread-safe access to configuration
  - Supports runtime configuration updates from remote control
  - Persists configuration changes back to YAML file atomically
  - Handles file permissions securely (0600 for sensitive configs)

#### Web UI (`web/ui/`)

- **React Dashboard** - Modern web interface:
  - Real-time visualization of connected agents and battery status
  - Remote command interface (start/stop discharge, set limits, force mode)
  - JSON editor for per-agent configuration overrides
  - Agent log viewer with streaming support
  - Download interface for agent binaries and updater
  - Authentication support for secure access

### Data Flow

1. **Local Operation**: Agent reads `config.yml`, starts discharge workers for each enabled battery, polls Sonnen API every 10 seconds, and executes scheduled discharge operations.

2. **Remote Monitoring**: When remote control is enabled, agent establishes WebSocket connection, streams telemetry snapshots, and receives commands. Control server aggregates telemetry and broadcasts to UI clients.

3. **Configuration Updates**: UI sends config update → Control server validates and stores → Pushes to connected agent → Agent applies changes and persists to local `config.yml`.

4. **Metrics Collection**: Controller updates observers → Observers update Prometheus gauges and snapshots → Metrics server exposes `/metrics` endpoint → Telemetry snapshots streamed to control server.

### Component Interaction

```
┌─────────────┐          ┌──────────────┐          ┌─────────────┐
│   Agent     │◄────────►│ Control      │◄────────►│  Web UI     │
│  (Device)   │ WebSocket│   Server     │ WebSocket│  (Browser)  │
└──────┬──────┘          └──────────────┘          └─────────────┘
       │
       │ HTTP API
       ▼
┌─────────────┐
│   Sonnen    │
│  Battery    │
│  Controller │
└─────────────┘
```

## Flexibility & Deployment

This service is designed with a versatile deployment nature. It can run on various platforms, hence making it adaptable to different use case scenarios:

- **Standalone Server**: The service can be installed and run on a standalone server in setups where higher computer power, storage, and network connectivity are required. Particularly useful in commercial or utility-scale deployments.

- **Low-power Devices like Raspberry Pi**: Our service is lightweight and does not require intensive computing resources. Therefore, it can be easily set up and run on low-power, cost-effective devices such as a Raspberry Pi. This could be a perfect solution for residential installations or small-scale deployments.

The ability to run on diverse platforms like a standalone server or a Raspberry Pi ensures that this service can cater to different needs, be it for a heavy-duty commercial setup or a small-scale residential use.

## Remote Monitoring & Control

The agent can optionally maintain a persistent WebSocket connection to a remote control plane for telemetry streaming and remote actions. Configure the `remote_control` block in `config.yml` (or environment variables):

- `enabled`: turn the feature on for the agent instance.
- `server_url`: WebSocket URL exposed by the control server (placed behind nginx for TLS).
- `shared_secret`: pre-shared token injected as an auth header for agent registration.
- `reconnect.initial_seconds` / `reconnect.max_seconds`: bounds for exponential reconnect backoff.

When disabled, the agent behaves as before and no remote traffic is emitted.

### Control Plane

Run the dedicated control server to aggregate agent telemetry, broker WebSocket sessions, and forward commands:

```bash
go run ./cmd/controlserver \
  -addr :8080 \
  -secret "<shared-secret>" \
  -static ./web/ui/dist
```

- `-addr` - Address to bind the control server (defaults to `:8080`)
- `-secret` - Shared secret required from agents (must match the agent `remote_control.shared_secret`)
- `-static` - Optional path to serve pre-built React UI assets (when provided, dashboard is hosted under `/app`)
- `-config-store` - Path to JSON file for per-agent configuration overrides (defaults to `data/agent-configs.json`)
- `-agent-binary` - Filename of agent binary in downloads directory (auto-detected if not provided)
- `-version-file` - Filename for version manifest served under `/downloads` (defaults to `VERSION`)

Agents connect to `/api/agent`, while the web UI consumes `/api/ui` for live updates.

Example nginx snippet for TLS termination:

```
server {
    listen 443 ssl;
    server_name control.example.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

### React Dashboard

The dashboard in `web/ui` provides a lightweight overview of connected agents and allows remote actions. Build it once and serve through the control server:

```bash
cd web/ui
npm install
npm run build
```

During development, run `npm run dev` (served on `http://localhost:5173`) with the built-in proxy to the Go control server at `http://localhost:8080`.

#### Remote Configuration Overrides

Each agent now supports live configuration updates pushed from the control server. The dashboard exposes a JSON editor per agent under **Remote configuration** where you can manage the authoritative `batteries` and `schedules` arrays. Saving changes stores the content in the control server's config store and immediately forwards the update to any connected agent over its WebSocket session. Agents fall back to their local `config.yml` when no override exists; otherwise the remote definition replaces the static file until another override is saved.

The editor enforces optimistic locking via the `revision` field—every successful save bumps the revision, and stale drafts are rejected with a conflict error. Config overrides persist on disk (see the `-config-store` flag) so agents that reconnect later receive the latest version automatically.

### Deployment Workflow

The repository includes `.github/workflows/deploy.yml`, a GitHub Actions pipeline that:

- builds the React bundle (`npm ci && npm run build`);
- cross-compiles the control server (`linux/amd64`) and agent binary (`linux/arm64`, Raspberry Pi ready);
- stages the agent inside `downloads/` beneath the static UI directory so it can be downloaded via browser or `curl`;
- pushes the bundle to a remote host over SSH and atomically updates `${DEPLOY_PATH}/current`;
- optionally executes a restart command (set the `DEPLOY_RESTART_CMD` secret, e.g. `sudo systemctl restart gok-controlserver`).

Configure the following repository secrets before running the workflow:

- `DEPLOY_HOST` – SSH hostname (or IP) of the remote control server.
- `DEPLOY_USER` – SSH username (must have write access to `DEPLOY_PATH`).
- `DEPLOY_SSH_KEY` – private key (OpenSSH format) with access to the host.
- `DEPLOY_PATH` – absolute target path on the host, for example `/opt/gok-pi`.
- `DEPLOY_RESTART_CMD` *(optional)* – command executed after each release to restart the service.

Trigger the workflow by pushing to `main` or manually via *Actions → Deploy Control Server → Run workflow*. The control server should be started with:

```bash
./controlserver -addr :8080 -secret "<shared-secret>" -static /opt/gok-pi/current/app
```

Additional optional flags:
- `-agent-binary <filename>` - Specify the agent binary filename in the downloads directory (auto-detected if not provided)
- `-version-file <filename>` - Filename for the version manifest (defaults to `VERSION`)
- `-config-store <path>` - Path to the agent configuration store JSON file (defaults to `data/agent-configs.json`)

### Agent Release Artifacts

The agent consumes a `gok` binary and a neighbouring `VERSION` manifest containing the SHA-256 hash of that binary. The control server now renders this manifest automatically by hashing the binary exposed under `/downloads`, so you only need to publish the binaries themselves during each deploy.

Build a release locally with the helper script:

```bash
./deploy/build_agent_release.sh \
  --output /tmp/gok-release \
  --binary-name gok-pi-agent-linux-arm64
```

The script produces `/tmp/gok-release/gok-pi-agent-linux-arm64`, `/tmp/gok-release/VERSION`, and `/tmp/gok-release/agentupdater`. Upload the binaries (`gok-pi-agent-linux-arm64` and `agentupdater`) to the hosting location exposed to agents (for example, the `/downloads` directory served by the control server). Pass `--upload 'scp "$1" "$2" "$3" user@host:/srv/downloads/'` to run a custom publish command automatically. The control server will expose `/downloads/VERSION` based on the uploaded agent binary.

With the GitHub Actions workflow, the ARM64 agent binary, updater, and manifest are exposed at `/downloads/gok-pi-agent-linux-arm64`, `/downloads/gok-agent-updater-linux-arm64`, and `/downloads/VERSION`. Devices can fetch them directly:

```bash
curl -o gok-pi-agent-linux-arm64 https://control.example.com/downloads/gok-pi-agent-linux-arm64
curl -o VERSION https://control.example.com/downloads/VERSION
curl -o agentupdater https://control.example.com/downloads/gok-agent-updater-linux-arm64
chmod +x gok-pi-agent-linux-arm64
chmod +x agentupdater
```

The React sidebar also surfaces a download button once the deployment workflow publishes the artifacts.

### Agent Auto Update

Install the updater binary on the device (for example, under `/opt/gok-pi/bin/agentupdater`) and configure a systemd unit to check for new releases on a schedule. When using the default deploy workflow, `agentupdater` is served alongside the agent binary under `/downloads/gok-agent-updater-linux-arm64`.

1. Copy the sample units:

   ```bash
   sudo cp deploy/agent-updater.service /etc/systemd/system/gok-agent-updater.service
   sudo cp deploy/agent-updater.timer /etc/systemd/system/gok-agent-updater.timer
   ```

2. Replace `{{AGENT_USER}}`, `{{AGENT_GROUP}}`, and `{{AGENT_PATH}}` in the service file with the real values (typically the same ones used for `gok-agent`).

3. Provide the download endpoints by editing `/etc/default/gok-agent-updater`:

   ```
   GOK_UPDATE_VERSION_URL=https://control.example.com/downloads/VERSION
   # Optional overrides:
   # GOK_UPDATE_BINARY_URL=https://control.example.com/downloads/gok-pi-agent-linux-arm64
   # GOK_UPDATE_BINARY_NAME=gok
   # GOK_UPDATE_TIMEOUT=45s
   # GOK_UPDATE_RESTART_SERVICE=gok-agent.service
   # GOK_UPDATE_RESTART_ENABLED=true
   ```

4. Reload systemd and activate the timer:

   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now gok-agent-updater.timer
   ```

The timer triggers `gok-agent-updater.service` 5 minutes after boot and every 30 minutes thereafter. The service runs the updater binary with the environment-derived URLs, compares the remote `VERSION` hash against the local copy, and only downloads the new binary when the hashes differ. Journal entries are tagged with `gok-agent-updater`.

Manual verification checklist:
- Stage a fake release by placing a binary and `VERSION` hash under a temporary HTTP server (e.g. `python -m http.server`).
- Run `agentupdater -agent-root $(pwd)/current -version-url http://localhost:8000/VERSION -binary-url http://localhost:8000/gok`.
- Observe the binary replacement and updated `VERSION` file under the agent root, and confirm the log output reports the new hash.

## Control Server Service Setup

Deploy the control server as a systemd service so it starts automatically and restarts on failure.

1. Copy `deploy/controlserver.service` to `/etc/systemd/system/gok-controlserver.service` on the host.
2. Replace the `{{DEPLOY_USER}}`, `{{DEPLOY_GROUP}}`, and `{{DEPLOY_PATH}}` placeholders with the real deploy account (e.g. `deploy`) and path (e.g. `/opt/gok-pi`).
3. Optionally create `/etc/default/controlserver` to override defaults exposed via environment variables:
   - `CONTROL_ADDR` – bind address (`:8080` by default).
   - `CONTROL_SECRET` – shared secret expected from agents.
   - `CONTROL_STATIC` – path to the React UI assets (defaults to `${DEPLOY_PATH}/current/app`).
4. Ensure the GitHub Actions deploy script lands releases under `${DEPLOY_PATH}/releases/<commit>` and updates `${DEPLOY_PATH}/current`. It already fixes ownership and permissions so nginx (`www-data`) can serve the UI.
5. Reload and enable the unit:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now gok-controlserver.service
```

Check the status with `sudo systemctl status gok-controlserver.service` and tail logs via `journalctl -u gok-controlserver.service -f`.

## Sonnen Controller's API

Sonnen's API provides a comprehensive set of controls and data for managing and monitoring a battery system. This includes functions for reading battery status, controlling battery charging and discharging, reading and setting battery parameters, and more. 

Visit [Sonnen website](https://sonnen.es/) for more info.

## License

This project is licensed under the MIT License. See the `LICENSE` file for details.
