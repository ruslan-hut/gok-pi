# GOK-Pi
## Service to control battery discharging

The aim of this project is to develop a service that automates battery discharging control using the Sonnen controller's API. The service fulfills two main objectives:

1. **Automation**: It simplifies the process of battery management by automating various tasks, such as monitoring battery status, controlling charging and discharging based on set parameters, and more.

2. **Optimization**: It utilizes the power of automation and real-time data to optimize the battery usage and durability. This potentially leads to more efficient energy use, cost savings, and prolongs battery life.

## Project Purpose and Scope

With the increasing importance of sustainable energy, battery storage and management is a critical component of the power grid. This Sonnen Battery Controller turns Controller's API usage into a more automated and simplified task.

This service could be utilized in residential, commercial, or utility-scale settings where Sonnen battery systems are installed. Potential uses include renewable energy storage, backup power, load shifting, and more.

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

- `-secret` must match the agent `remote_control.shared_secret`.
- `-static` is optional; when provided the built React dashboard is hosted under `/app`.
- Agents connect to `/api/agent`, while the web UI consumes `/api/ui` for live updates.

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
./gok -addr :8080 -secret "<shared-secret>" -static /opt/gok-pi/current/app
```

### Agent Release Artifacts

The agent consumes a `gok` binary and a neighbouring `VERSION` file that stores the SHA-256 hash of that binary. Publish both artifacts during each deploy so devices can discover updates without downloading the entire binary every time.

Build a release locally with the helper script:

```bash
./deploy/build_agent_release.sh \
  --output /tmp/gok-release \
  --binary-name gok-pi-agent-linux-arm64
```

The script produces `/tmp/gok-release/gok-pi-agent-linux-arm64`, `/tmp/gok-release/VERSION`, and `/tmp/gok-release/agentupdater`. Upload these files to the hosting location exposed to agents (for example, the `/downloads` directory served by the control server). Pass `--upload 'scp "$1" "$2" "$3" user@host:/srv/downloads/'` to run a custom publish command automatically.

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
