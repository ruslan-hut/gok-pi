# Battery Scan

When a battery stops answering, the agent log says only that it is unreachable:

```
level=ERROR msg="checking battery status" battery=BAT-001 \
  error="request failed after 5 retries: Get \"http://192.168.68.107/api/v2/status\": \
  dial tcp 192.168.68.107:80: connect: no route to host"
```

That line is the same whether the battery took a new DHCP lease or is genuinely
off the air, and the two want opposite responses — edit an address, or walk over
to the hardware. Only the device has a route to the battery's network, so the
scan runs there.

```
UI "Find battery" ──POST /api/agents/<id>/scan-battery──▶ control server
                                                               │
                                              ws: server.scan.request
                                                               ▼
                                                   agent sweeps the range
                                                               │
                                              ws: agent.scan.response
                                                               ▼
      192.168.68.142  ✓ sonnen: usoc 61%, rsoc 59%, mode 2   ◀── UI
      192.168.68.107  ✗ received status code: 404  (configured)
```

## Using it

**Tools** tab → **Find battery**. Visible to logged-in users only; the scan is
read-only on the battery but it puts traffic on someone's LAN, so it is not
something an anonymous reader of the dashboard can start.

Leave **Address range** empty for the common case: the agent sweeps the /24
around the battery's configured address, which is where a battery with a new
lease nearly always is. The field also accepts

| Form | Meaning |
|---|---|
| `192.168.1.0/24` | a CIDR prefix, host addresses only |
| `192.168.1.100-150` | a short range over the last octet |
| `192.168.1.100-192.168.1.150` | an explicit range |
| `192.168.1.107` | a single address |

Any of these may be combined with commas. A spec covering more than
`netscan.MaxAddresses` (1024) is refused rather than truncated: a sweep that
quietly skipped half a subnet would report "not found" about addresses it never
looked at.

The verdict line is the result. One of three things:

- **Found at an address that is not the configured one** — the battery moved.
  Update the battery URL in **Configure**; the agent restarts its workers on the
  new address without a process restart.
- **Found at the configured address** — the address is not the problem. The
  battery is reachable and answering, so look at the schedule, the token, or
  what the battery itself is doing.
- **Nothing answered** — the battery is not on that range. Check power and
  cabling, or widen the range if the subnet may have changed.

Hosts that answered but are not the battery are listed too. That list is how you
learn what took the battery's old lease — a `404` from some other device's web
server is the usual culprit, and it is also what makes the agent log flap between
error and recovery if a stale poller is still pointed at it.

## How the sweep works

Two stages, for a reason worth knowing before changing the defaults.

**Stage one** is a plain TCP connect to the battery's port on every address, 64
in flight, 400 ms each. A /24 finishes in a few seconds.

**Stage two** runs a real driver probe — `driver.New` with the candidate address
and the battery's configured token, then `Status()` — but only against the
addresses that answered stage one. The probe is what distinguishes the battery
from anything else listening: it speaks the vendor's protocol, it authenticates,
and it comes back with the state of charge, which is what tells an operator the
device found is *their* battery.

The split exists because a driver probe carries the vendor's own retry policy.
The Sonnen driver retries five times with linear backoff — up to the better part
of a minute against a host that accepts connections and then says nothing — so
probing every address in a /24 would take hours. Stage one reduces the set to the
handful of hosts actually listening.

Probes run serially, not in parallel: there are only ever a few responders, and a
battery answering a status request while another probe hammers it is how a
diagnostic turns into the outage it was meant to diagnose.

A probe that ignores its deadline is abandoned after `ProbeTimeout` (8 s) and
reported as a timeout. The agent caps a whole sweep at 90 s and refuses a second
concurrent sweep; the control server waits 2 minutes for the reply.

## Code map

| Piece | Where |
|---|---|
| Range parsing, two-stage sweep | `internal/netscan` |
| RPC types, one-at-a-time guard, handler | `internal/remote/wsclient/scan.go` |
| Driver probe, battery/URL resolution | `cmd/gok/batteryscan.go` |
| `requestBatteryScan`, message dispatch | `remote/server/agent.go` |
| `POST /api/agents/<id>/scan-battery` | `remote/server/server.go` |
| Panel | `web/ui/src/components/tools/BatteryScan.tsx` |

`netscan` knows nothing about any vendor: the probe is injected, which keeps the
driver registry and the battery token out of both the transport and the sweep,
and makes the sweep testable without a battery.

A new driver needs no work here. It is probed through `driver.Driver.Status()`
like any other, as long as its config carries an address the sweep can rebuild —
see `batteryTarget.urlFor`.

## Prevention

The scan diagnoses a moved battery; it does not stop the battery moving. Give the
battery a DHCP reservation on its MAC, or a static address. A battery that keeps
taking new leases will eventually hand its old address to something else, which
is the state this tool exists to recognise.
