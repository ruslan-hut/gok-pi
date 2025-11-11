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
./controlserver -addr :8080 -secret "<shared-secret>" -static /opt/gok-pi/current/app
```

With this layout, the ARM64 agent binary is exposed at `/downloads/gok-pi-agent-linux-arm64`. Devices can download it directly:

```bash
curl -o gok-pi-agent-linux-arm64 https://control.example.com/downloads/gok-pi-agent-linux-arm64
chmod +x gok-pi-agent-linux-arm64
```

The React sidebar also surfaces a download button once the deployment workflow publishes the binary.

## Sonnen Controller's API

Sonnen's API provides a comprehensive set of controls and data for managing and monitoring a battery system. This includes functions for reading battery status, controlling battery charging and discharging, reading and setting battery parameters, and more. 

Visit [Sonnen website](https://sonnen.es/) for more info.

## License

This project is licensed under the MIT License. See the `LICENSE` file for details.
