# Raspberry Pi agent setup

Setting up a Raspberry Pi as an on-site agent: the Pi sits on the battery LAN
and connects out to the control server, so the site needs only an ordinary
internet connection. No VPN, public IP or port forwarding is required, and it
works behind CGNAT or a double NAT.

General build, config and systemd reference is in [deployment.md](deployment.md);
this page is the step-by-step for one device. The last section covers moving an
existing agent (Merce 5) from a server reaching the batteries over L2TP onto a Pi.

## Hardware

- Any 64-bit Pi: Pi 4 (1–2 GB is plenty), Pi 5, or Pi Zero 2 W. The agent uses
  well under 100 MB of RAM.
- Wired Ethernet to the battery LAN. The Zero 2 W needs a micro-USB Ethernet
  adapter or a hub with RJ45.
- The power supply matched to the board (Pi 4: 5.1 V 3 A USB-C; Pi 5: 27 W).
  Undervoltage resets on a battery controller are worth avoiding.
- A high-endurance microSD card (Samsung PRO Endurance, SanDisk High Endurance).
  The agent writes logs continuously and kit cards wear out.

The original Pi Zero / Zero W (ARMv6) is not supported by the release build.

## 1. Flash the OS

Raspberry Pi Imager → **Raspberry Pi OS Lite (64-bit)**. In the OS customisation
dialog:

- hostname: site name, e.g. `merce5-gok`
- user: an admin account (not the service user)
- SSH: enabled, public-key only, paste your key
- locale/timezone: the site timezone (e.g. `Europe/Madrid`)
- WiFi: leave empty when the Pi is on Ethernet

## 2. First boot

Plug into the battery LAN. Find the Pi in the router's DHCP leases and make that
lease static.

```bash
ssh <admin>@<pi-ip>
sudo apt update && sudo apt full-upgrade -y
timedatectl                     # "System clock synchronized: yes", correct time zone
```

The Pi has no RTC: after a power cut the schedules depend on NTP, so check the
clock is synchronised before leaving the site.

Cap the journal so it doesn't fill the card:

```bash
sudo mkdir -p /etc/systemd/journald.conf.d
printf '[Journal]\nSystemMaxUse=100M\n' | sudo tee /etc/systemd/journald.conf.d/size.conf
sudo systemctl restart systemd-journald
```

## 3. Reachability of the batteries

Before installing anything, confirm the Pi can reach every battery API directly:

```bash
curl -s -m5 -H "Auth-Token: <token>" http://<battery-ip>/api/v2/status
```

Each must return Sonnen status JSON. Give every battery a static IP or DHCP
reservation on the site router; the agent config addresses them by IP.

## 4. Service user and layout

```bash
sudo useradd --system --home /opt/gok --shell /usr/sbin/nologin gok
sudo mkdir -p /opt/gok/current /opt/gok/bin
```

```
/opt/gok/
├── config.yml        agent config; the agent rewrites it on UI changes
├── current/gok       agent binary (replaced by the updater)
└── bin/agentupdater  updater binary
```

The agent saves config with write-to-temp-then-rename, so `gok` must own
`/opt/gok` itself, not only `config.yml`.

## 5. Install the binaries

```bash
sudo curl -fsSo /opt/gok/current/gok https://bat.wattbrews.es/downloads/gok-pi-agent-linux-arm64
sudo curl -fsSo /opt/gok/bin/agentupdater https://bat.wattbrews.es/downloads/gok-agent-updater-linux-arm64
sudo chmod 755 /opt/gok/current/gok /opt/gok/bin/agentupdater
```

Or build locally with `./deploy/build_agent_release.sh` (defaults to
`linux/arm64`) and `scp` the files over.

## 6. Config

New device: write `/opt/gok/config.yml` with the fields listed in
[deployment.md](deployment.md#configuration). Leave `device_id` empty and the
agent generates one on first start. Minimal shape:

```yaml
device_name: <site name>
env: prod
timezone: Europe/Madrid
remote_control:
  enabled: true
  server_url: wss://bat.wattbrews.es/api/agent
  shared_secret: <control server secret>
batteries:
  - name: "001"
    driver: sonnen
    url: http://<battery-ip>/api/v2
    token: <sonnen token>
    enabled: true
    capacity_limit: 20000
schedules: []
charge_schedules: []
```

Schedules and the rest can then be set from the UI.

Replacing an existing agent: copy its `config.yml` instead, so `device_id` and
everything attached to it in the UI carries over. See the migration section.

```bash
sudo chown -R gok:gok /opt/gok
sudo chmod 600 /opt/gok/config.yml
```

## 7. Agent service

The unit and updater keep the names from `deploy/` (`agent.service`,
`agent-updater.service`, `agent-updater.timer`): the timer's `Unit=` and the
updater's default restart target refer to these names.

```bash
sed -e 's/{{AGENT_USER}}/gok/; s/{{AGENT_GROUP}}/gok/; s#{{AGENT_PATH}}#/opt/gok#g' \
  deploy/agent.service.example | sudo tee /etc/systemd/system/agent.service
sudo systemctl daemon-reload
sudo systemctl enable --now agent.service
journalctl -u agent -f
```

Run the `sed` on a checkout of the repo, or copy the file over and run it on
the Pi. Hold off on `enable --now` when migrating until the old agent is stopped
(see below).

## 8. Auto update

```bash
sed -e 's/{{AGENT_USER}}/gok/; s/{{AGENT_GROUP}}/gok/; s#{{AGENT_PATH}}#/opt/gok#g' \
  deploy/agent-updater.service.example | sudo tee /etc/systemd/system/agent-updater.service
sudo cp deploy/agent-updater.timer /etc/systemd/system/

sudo tee /etc/default/gok-agent-updater >/dev/null <<'EOF'
GOK_UPDATE_VERSION_URL=https://bat.wattbrews.es/downloads/VERSION
GOK_UPDATE_BINARY_URL=https://bat.wattbrews.es/downloads/gok-pi-agent-linux-arm64
GOK_UPDATE_AGENT_ROOT=/opt/gok/current
GOK_UPDATE_RESTART_SERVICE=agent.service
EOF
```

`GOK_UPDATE_BINARY_URL` must be set: the default (VERSION directory + `gok`)
does not exist on the control server.

The updater runs as `gok` and calls `systemctl restart agent.service`; allow
that one action with polkit:

```bash
sudo tee /etc/polkit-1/rules.d/50-gok-agent.rules >/dev/null <<'EOF'
polkit.addRule(function(action, subject) {
  if (action.id == "org.freedesktop.systemd1.manage-units" &&
      action.lookup("unit") == "agent.service" &&
      action.lookup("verb") == "restart" &&
      subject.user == "gok") {
    return polkit.Result.YES;
  }
});
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now agent-updater.timer
sudo systemctl start agent-updater.service && journalctl -u agent-updater -n 20
```

## 9. Verify

- `systemctl status agent` is active; `journalctl -u agent` shows battery polls
  without errors.
- The device shows **Connected** in the UI, with telemetry for every battery
  under Monitor.
- `systemctl list-timers agent-updater.timer` shows the next run.
- Pull the power once and confirm the agent comes back connected with the
  correct time.

## 10. Remote access (optional)

Without a VPN into the site there is no way to SSH to the Pi from outside. The
agent can be restarted from the UI and updates itself, but for shell access
install an outbound overlay such as Tailscale, WireGuard or ZeroTier on the Pi.
They work through CGNAT.

## Migrating an existing agent: Merce 5

Before: the agent ran on `dev` (136.144.220.120) and reached the batteries
through the site MikroTik's L2TP/IPsec tunnel (router at `10.0.81.101`, port
forwards 8010–8012). The site moved to a Vodafone uplink behind a second NAT
and IPsec stopped completing, so the agent moves on site.

Battery URLs, taken from the MikroTik dst-nat rules:

| Battery | On `dev` | On the Pi |
|---|---|---|
| 001 | `http://10.0.81.101:8010/api/v2` | `http://192.168.8.10/api/v2` |
| 002 | `http://10.0.81.101:8011/api/v2` | `http://192.168.8.11/api/v2` |
| 003 | `http://10.0.81.101:8012/api/v2` | `http://192.168.8.12/api/v2` |

The Pi joins the MikroTik LAN (`192.168.8.0/24`); the MikroTik stays as the
site router.

1. On `dev`, copy the live config and rewrite the URLs:

   ```bash
   systemctl cat agent | grep -E 'GOK_CONF|ExecStart'     # locate config.yml
   CONF=/opt/gok/config.yml                               # adjust
   cp "$CONF" ~/merce5-pi-config.yml
   sed -i -E 's#http://10\.0\.81\.101:80(1[0-2])/api/v2#http://192.168.8.\1/api/v2#' ~/merce5-pi-config.yml
   diff "$CONF" ~/merce5-pi-config.yml                     # exactly three url lines
   ```

2. Set up the Pi through step 6 with that file as `/opt/gok/config.yml`, and
   run step 3 against `192.168.8.10`, `.11` and `.12`. Make the three Sonnen
   leases static on the MikroTik.
3. Cut over. Two agents with the same `device_id` fight over the control
   server connection, so stop the old one first:

   ```bash
   # dev
   sudo systemctl disable --now agent agent-updater.timer
   # Pi
   sudo systemctl enable --now agent agent-updater.timer
   ```

4. In the UI, Merce 5 (`63dad649378c6311`) reconnects and Configure →
   Batteries shows the `192.168.8.x` URLs.
5. Clean up: disable the MikroTik L2TP client (`l2tp-wattbrews`) and remove the
   `merce` login from `/etc/ppp/chap-secrets` on `dev`.

Rollback: stop the agent on the Pi, re-enable the L2TP client, start the agent
on `dev` again. Its config still has the old URLs.
