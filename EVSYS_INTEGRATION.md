# EV Charger Integration (evsys)

When a customer starts a charging session on an EV charger managed by
[evsys](https://github.com/ruslan-hut/evsys), the linked battery switches to
discharge so the car draws stored energy instead of grid power. When the session
ends, the battery is released back to its normal schedule.

```
evsys transaction.start ──POST /api/webhooks/evsys──▶ control server
                                                          │
                                        agent.command start_discharge
                                                          ▼
                                                   agent ──▶ battery
```

No evsys code changes are required — it already emits the events we need.

## 1. Enable webhooks in evsys

In the evsys `config.yml` (requires `mongo.enabled: true`, since subscribers live
in the database):

```yaml
webhooks:
  enabled: true
```

Then register this control server as a subscriber:

```js
db.webhook_subscribers.insertOne({
  name: "gok-pi",
  url: "https://<control-server>/api/webhooks/evsys",
  token: "<the same value as GOK_CHARGER_TOKEN below>",
  events: ["transaction.start", "transaction.stop"],
  is_enabled: true,
  updated_at: new Date()
})
```

evsys delivers through a Mongo-backed outbox: ordered per subscriber, retried
with backoff (30s, 1m, 5m, 15m, 1h, then hourly) and abandoned after 24h. It
sends the secret as `Authorization: Token <token>`.

## 2. Configure the control server

```yaml
charger_token: "<shared secret>"           # env: GOK_CHARGER_TOKEN
charger_links: data/charger-links.json     # optional, this is the default
charger_sessions: data/charger-sessions.json
```

**An empty `charger_token` disables the webhook endpoint entirely** (it returns
404). Unlike the agent shared secret and the UI login, it does not fail open:
the endpoint drives the battery and is reachable from the public internet.

## 3. Link chargers to batteries

Edit the links in the web UI (Config → Charger links), or `PUT` them to
`/api/charger-links`:

```json
[
  {
    "name": "site-a",
    "enabled": true,
    "location_id": "loc-01",
    "charge_point_ids": ["Wallbox3", "Wallbox4"],
    "agent_id": "a1b2c3d4e5f6a7b8",
    "battery_name": "battery1",
    "power_limit": 3000,
    "soc_limit": 40,
    "max_duration_min": 240
  }
]
```

| Field | Meaning |
|---|---|
| `location_id` | evsys location; the primary match key |
| `charge_point_ids` | fallback match keys — **required for OCPP 2.0.1 chargers**, whose events carry no location |
| `agent_id` | the agent's `device_id` from its `config.yml` |
| `battery_name` | which battery to discharge |
| `power_limit` | discharge rate in W |
| `soc_limit` | SoC floor in %; the battery never discharges below it |
| `max_duration_min` | safety cap; 0 disables it |

`agent_id` and `battery_name` must match a real agent and battery — nothing
validates that they exist, and a typo simply produces commands that go nowhere.

## Behaviour

- **A charging session wins over schedules.** The discharge runs as an override
  that bypasses the time windows, and the normal schedule resumes when the
  session ends.
- **It survives config pushes.** The server pushes config whenever auto-schedules
  change; an operator's manual override is cancelled by that, but a
  charger-driven one is not — only the session that started it ends it.
- **Duplicates are harmless.** evsys delivers at-least-once. A repeated
  `transaction.start` is ignored, and a `transaction.stop` for a session we never
  saw is ignored.
- **Concurrent sessions share the battery.** Discharge starts when the first car
  arrives and stops when the last one leaves, at the highest configured power and
  the most conservative SoC floor among the active sessions.
- **Offline agents are not forgotten.** The session stays recorded and the
  discharge is re-asserted when the agent reconnects — which also covers an agent
  that restarted and lost its in-memory override.
- **A lost stop cannot strand the battery.** `max_duration_min` bounds how long a
  session may hold it; a sweeper releases it and logs a warning.
- **The SoC floor is honoured.** A session that would discharge below it is
  refused and logged; the car falls back to grid power.

## Observability

- `GET /api/charger-sessions` lists the active sessions; the UI also receives a
  `charger.sessions` broadcast on every change.
- Telemetry carries `override_source` (`"charger"` or `"manual"`) per battery, so
  the UI shows why a battery is discharging.
- Delivery state is visible on the evsys side in `db.webhook_outbox`.

## Testing without evsys

The receiver is plain HTTP:

```bash
curl -X POST http://localhost:8080/api/webhooks/evsys \
  -H "Authorization: Token <GOK_CHARGER_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{"id":"e1","type":"transaction.start","source":"evsys","sequence":1,
       "time":"2026-08-04T10:00:00Z",
       "data":{"charge_point_id":"Wallbox3","connector_id":1,
               "location_id":"loc-01","transaction_id":12345,"id_tag":"TAG1"}}'
```

Send the same body with `"type":"transaction.stop"` to release the battery.

## Known limitation

evsys emits no meter/power events — `OnMeterValues` updates the database but
notifies no listeners, and only the cumulative `consumed` figure arrives on stop.
Discharge therefore runs at the fixed `power_limit` from the link rather than
tracking what the car is actually drawing. Matching live demand would require
adding a meter event to evsys first.
