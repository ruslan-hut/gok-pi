# Documentation

| Document | Contents |
|---|---|
| [architecture.md](architecture.md) | Executables, package map, control loop, drivers, remote protocol, config sync, metrics |
| [deployment.md](deployment.md) | Control server config, CI deploy, systemd units, agent build, config and auto-update |
| [evsys-integration.md](evsys-integration.md) | EV charger webhooks: evsys setup, charger↔battery links, override behaviour |
| [huawei-integration.md](huawei-integration.md) | Huawei LUNA2000B over Modbus-TCP: site, validated point table, SmartLogger dispatch registers and handover options, bring-up stages |
| [backlog.md](backlog.md) | Open deferred items from the stability review |

Reference material:

- [huawei/LUNA2000B ESS Modbus Port Definitions.pdf](huawei/LUNA2000B%20ESS%20Modbus%20Port%20Definitions.pdf) — vendor register document, issue 01
- [huawei/regen_tables.py](huawei/regen_tables.py) — re-extracts the point and alarm tables from that PDF
- [huawei/SmartLogger V300R024C10 ModBus Interface Definitions.pdf](huawei/SmartLogger%20V300R024C10%20ModBus%20Interface%20Definitions.pdf) — logger (unit 0) and power meter registers, issue 54

Elsewhere in the repo:

- [../README.md](../README.md) — project overview and quick start
- [../CLAUDE.md](../CLAUDE.md) — working notes for Claude Code
- [../scripts/README.md](../scripts/README.md) — price export script
