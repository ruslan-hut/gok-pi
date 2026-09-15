# Backlog

Deferred items left open by the stability review (tiers 1–3). Everything else
from that review is merged. Severity: HIGH / MED / LOW.

- **I-MED** Session DB writes happen under `st.mu` in `remote/server/sessions.go`.
  Move them to an async persist queue; correctness-sensitive, needs its own change.
- **H-MED** In-memory fallback `autoschedule.GenerateSchedules` compares stop
  hours against `time.Now()` in server-local time instead of the price market's
  timezone (`electricity/autoschedule/generator.go`). Check whether the fallback
  path is still reachable first.
- **B-MED** `redata` parses datetimes with one fixed layout
  (`electricity/redata/client.go`); tolerate `Z`, missing millis and offsets
  without a colon.
- **C-LOW** `Schedule.Validate` has no upper power bound — there is no
  hardware-max field in config to clamp against.
- **A-LOW** Dead controller state: `capacity`, `stopTime` fields and the no-op
  `SetCapacityLimit` in `battery/controller/controller.go`.
