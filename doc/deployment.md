# Deployment

## Control server

### Configuration

`gok-cs -config /etc/gok-cs/config.yml`. Without `-config` every value comes
from environment variables. Template: `deploy/gok-cs.config.example`.

| Key | Env | Default | Purpose |
|---|---|---|---|
| `addr` | `CONTROL_ADDR` | `:8080` | bind address |
| `secret` | `CONTROL_SECRET` | — | shared secret agents must present |
| `static` | `CONTROL_STATIC` | — | built UI directory, served under `/app` |
| `agent_binary` | `GOK_CONTROL_AGENT_BINARY` | auto-detected | agent file under `/downloads` |
| `version_file` | `GOK_CONTROL_VERSION_FILE` | `VERSION` | manifest name under `/downloads` |
| `config_store` | | `data/agent-configs.json` | per-agent config store |
| `session_db` | | `data/sessions.db` | SQLite session/history DB |
| `ui_username` / `ui_password` | `GOK_UI_USERNAME` / `GOK_UI_PASSWORD` | — | UI login; empty disables auth |
| `log_file` | `GOK_CS_LOG_FILE` | stdout | |
| `email_provider` | | disabled | Brevo credentials for reports and alerts |
| `email_state` | | `data/email-reports-state.json` | last-sent report dates |
| `charger_token`, `charger_links`, `charger_sessions` | `GOK_CHARGER_TOKEN` | — | see [evsys-integration.md](evsys-integration.md) |

Agents connect to `/api/agent`, the UI to `/api/ui`. TLS is terminated by nginx;
`deploy/nginx.cfg` is an example that also forwards the WebSocket upgrade.

### CI deploy

`.github/workflows/deploy.yml` runs on every push to `master`:

1. builds the UI, the control server (`linux/amd64`), and the agent and updater
   (`linux/arm64`);
2. uploads a bundle to `${DEPLOY_PATH}/releases/<sha>`, installs
   `/usr/local/bin/gok-cs`, points `${DEPLOY_PATH}/current` at the release and
   keeps the three newest releases;
3. **regenerates `/etc/gok-cs/config.yml` from repository secrets** — manual
   edits on the host are overwritten; data lives in `/var/lib/gok-cs/`;
4. runs `DEPLOY_RESTART_CMD` if set.

Secrets: `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_SSH_KEY`, `DEPLOY_PATH`,
`DEPLOY_RESTART_CMD` (optional), `CONTROL_ADDR`, `CONTROL_SECRET`,
`CONTROL_AGENT_BINARY`, `GOK_UI_USERNAME`, `GOK_UI_PASSWORD`,
`GOK_BREVO_ENABLED`, `GOK_BREVO_API_KEY`, `GOK_BREVO_SENDER_NAME`,
`GOK_BREVO_SENDER_EMAIL`, `GOK_CHARGER_TOKEN`.

The agent binary and updater are published at
`/downloads/gok-pi-agent-linux-arm64` and
`/downloads/gok-agent-updater-linux-arm64`; the server renders
`/downloads/VERSION` by hashing the agent binary.

### systemd

1. Copy `deploy/controlserver.service` to `/etc/systemd/system/gok-controlserver.service`.
2. Replace `{{DEPLOY_USER}}` and `{{DEPLOY_GROUP}}` (CI sets file ownership to `www-data`).
3. `sudo systemctl daemon-reload && sudo systemctl enable --now gok-controlserver.service`

Logs go to `log_file` (`/var/log/gok-cs.log` in CI-generated config).

## Agent

### Build

```bash
./deploy/build_agent_release.sh -o /tmp/gok-release --binary-name gok-pi-agent-linux-arm64
```

Produces the agent binary, `VERSION` and `agentupdater`. `--upload '<cmd>'` runs
a publish command with the three paths as `$1 $2 $3`. Or download from a
deployed server:

```bash
curl -o gok https://control.example.com/downloads/gok-pi-agent-linux-arm64
curl -o agentupdater https://control.example.com/downloads/gok-agent-updater-linux-arm64
chmod +x gok agentupdater
```

### Configuration

`config.yml` (cleanenv; env vars override, e.g. `REMOTE_CONTROL_SHARED_SECRET`):

- `device_id`, `device_name`, `env` (`local`/`dev`/`prod`), `timezone`
- `batteries[]` — `name`, `driver`, `url`, `token`, `enabled`, `capacity_limit`,
  `power_limit`, `soc_limit`, `auto_schedule`
- `schedules[]`, `charge_schedules[]` — windows with `power_limit`, `soc_limit`, `run_once`
- `remote_control` — `enabled`, `server_url` (`wss://…/api/agent`),
  `shared_secret` (must match the server `secret`),
  `reconnect.initial_seconds` / `reconnect.max_seconds` (5 / 60)
- `metrics` — `enabled`, `bind`, `port`

With remote control enabled, config edited in the UI is written back to this file.

### systemd

1. Copy `deploy/agent.service.example` to `/etc/systemd/system/gok-agent.service`;
   replace `{{AGENT_USER}}`, `{{AGENT_GROUP}}`, `{{AGENT_PATH}}`.
2. Optional overrides in `/etc/default/gok-agent` (template:
   `deploy/gok-agent.env.example`).
3. `sudo systemctl daemon-reload && sudo systemctl enable --now gok-agent.service`

Keep `Restart=always` (or at least `on-failure`): remote restart exits with 75.

### Auto update

1. Copy `deploy/agent-updater.service` and `deploy/agent-updater.timer` to
   `/etc/systemd/system/gok-agent-updater.{service,timer}`; replace the
   `{{AGENT_*}}` placeholders.
2. Create `/etc/default/gok-agent-updater` (template:
   `deploy/gok-agent-updater.env.example`), at minimum
   `GOK_UPDATE_VERSION_URL=https://control.example.com/downloads/VERSION`.
3. `sudo systemctl daemon-reload && sudo systemctl enable --now gok-agent-updater.timer`

The timer fires 5 minutes after boot and every 30 minutes; the binary is
downloaded only when the hash differs. Journal tag `gok-agent-updater`.

Manual test against a local HTTP server:

```bash
agentupdater -agent-root $(pwd)/current \
  -version-url http://localhost:8000/VERSION -binary-url http://localhost:8000/gok
```
