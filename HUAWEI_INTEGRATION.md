# Huawei LUNA2000B Integration (Modbus-TCP)

Support for Huawei LUNA2000B C&I energy storage systems, reached over Modbus-TCP.
Status: **reading works on the real site; nothing writes yet.** Modbus TCP is
enabled on the SmartLogger, both ESS units answer, and the point table is
validated against them (2026-09-15). No driver exists.

Source document: `doc/LUNA2000B ESS Modbus Port Definitions.pdf`, issue 01
(2025-09-10). Section numbers below refer to it.

```
gok agent ──Modbus-TCP:502──▶ SmartLogger 10.0.80.91 ──▶ unit 5  ESS LUNA2000B-V2
   (master)                    (unit 0)                ──▶ unit 6  ESS LUNA2000B-V2
                                                       ──▶ unit 1  Inverter SUN2000-50KTL-M3
                                                       ──▶ unit 11 PowerMeter
```

## Target site

| | |
|---|---|
| Plant | Electrolinera_Pedernoso (EV charging station) |
| Logger | SmartLogger, serial `102597484606`, `10.0.80.91` |
| ESS | `ESS(Net.8.129)`, `ESS(Net.8.130)` — 2 × 215 kWh = 430.080 kWh nominal |
| Also on the logger | `Inverter(COM1-1)`, `Meter-AM0010259748…` |
| External | `87.247.129.133:27250` → `10.0.80.91:27250` (NAT rule) |
| Access | VPN, routed; laptop lands on `10.0.80.155` |
| State | In production service, cycling daily |

Being an EV charging station, this site is a natural fit for the existing
charger↔battery links — see `EVSYS_INTEGRATION.md`.

## Site bring-up, 2026-09-15

### Logger configuration now in place

Changed in the SmartLogger UI, **Ajustes → Parámetros de comunicación → Modbus TCP**:

- Modbus TCP enabled (it was disabled — see the diagnosis below).
- **Modo de dirección: dirección lógica.** In communication-address mode only the
  RS485 devices (inverter 1, meter 11) had unit IDs; the ESS cabinets are
  network-attached and were unreachable at every unit 1–247. Logical-address
  mode gives them **5 and 6**.

Logger firmware `V300R024C10SPC161`. No TLS was needed.

### Device list (unit 0, `0x2B`/`0x0E` code 3)

| Unit | Model | Software | ESN |
|---|---|---|---|
| 0 | Smart Logger | V300R024C10SPC161 | 102597484606 |
| 1 | SUN2000-50KTL-M3 | V200R023C00SPC125 | ES2320029926 |
| 11 | PowerMeter | V100R001C01AM001 | AM00102597484606 |
| 5 | LUNA2000B-V2 | V200R024C00SPC400 | BT2610479858 |
| 6 | LUNA2000B-V2 | V200R024C00SPC400 | BT2610378535 |

Unit numbers for the ESS come from the logger UI; the device list itself reports
device ID 0 for both, as it does for any network-attached device. Which ESN is
unit 5 and which is unit 6 is not established.

The logger departs from the document in three ways, all handled in
`internal/modbus`: it sets the object count to the list total while sending one
object per response, sends object 0x87 as a binary integer rather than text,
and pads descriptions past the 260-byte recommended frame.

### Point table validated

`essprobe dump -all` on units 5 and 6: **92 of 92 registers read, none failed**,
every value plausible. `RatedCapacity` reads **215.04 kWh** on each, and
2 × 215.04 = 430.08 kWh is exactly the nominal capacity FusionSolar shows.
Pmax / RPmax read ±140.4 kW; charge cut-off 100 %, discharge cut-off 5 %;
working mode PQ; no alarms raised on either unit.

### Sign conventions — confirmed

Sampled read-only every 30 s for 4 minutes, midday, both units charging from PV:
control SOC rose 92.0 → 92.6 %, *energy charged today* grew 1.36 kWh (≈ 20.4 kW,
matching the measured power) while *energy discharged today* stayed flat.

| Register | While charging | Convention |
|---|---|---|
| 30417 Charge/Discharge power | +20 kW | battery side: **positive = charge** |
| 32037 Rack current | +24.6 A | positive = charge |
| 32986 Active power | −20.4 kW | AC side: **negative = charge** |
| 42913 Active power (%) | −14.5 % | **negative = charge** |

The two conventions are opposite: 30417 is measured at the battery, 32986 and the
setpoints at the AC terminal. `42913 × 42936 / 100` reproduces 32986 to within
0.1 kW (−14.58 % × 140.4 kW = −20.47 kW), so the % setpoint shares the AC-side
convention. **42915 (kW) is inferred to follow the same convention, negative =
charge, but has not been written, so that remains unverified.**

### The dispatcher is the SmartLogger, via 42913

On both units `42915` (kW setpoint) reads 0, while `42913` (active power %) holds
the live dispatch and moves between samples (−14.45 … −14.61 %, 30 s apart).
Work status is `0x0204 Running: limited power`. So the SmartLogger runs a
continuous control loop and commands the cabinets through the **percentage**
register against a 140.4 kW baseline, not the kW register. Anything we write to
either register will be overwritten on its next cycle unless the logger's own
ESS control is switched off or handed over.

## Original blocker: Modbus TCP was disabled on the SmartLogger

Measured from `10.0.80.155` over the VPN:

| Check | Result | Meaning |
|---|---|---|
| `ping 10.0.80.91` | 21 ms, 0% loss | routing is fine |
| `:443` | open, TLS cert `CN=102597484606` | the logger, serial matches FusionSolar |
| `:502` | **TCP RST in 0.02 s** | nothing listening |
| `:27250` (LAN and via NAT) | TCP RST | NAT rule works, port not served |
| `10.0.80.129`, `10.0.80.130` | no ICMP, TCP timeout | ESS cabinets are not on this subnet |

A reset that fast, from a host that serves 443 happily, means the Modbus TCP
slave is switched off — not a busy socket, not a client limit, not a firewall.
A connection limit would accept and close; a firewall would time out.

Port 27250 is the port the SmartLogger **dials out on** to reach FusionSolar. It
is not a service the logger offers inbound, so the NAT rule reaches a closed
port. Harmless, but it is not a path to Modbus. (§4.2.1.4 describes a reverse
mode where the device dials out to a master on the public internet; that is a
fallback if inbound ever becomes impossible, and is not needed while the VPN
works.)

Since `10.0.80.129/.130` answer nothing, the cabinets are on the logger's
downstream segment rather than the LAN: **the logger is the only path**, and the
two ESS units are addressed by Modbus unit ID behind it.

### The change to make

SmartLogger web UI at `https://10.0.80.91`, installer account:

**Ajustes → Parámetros de comunicación → Modbus TCP**

1. **Configuración de enlace**: `Deshabilitar` → `Habilitar (ilimitado)`, or
   `Habilitar (n)` with the agent host in the client address list.
2. **Modo de dirección**: note whether it is *dirección de comunicación* or
   *dirección lógica*. This decides what unit IDs the ESS units answer on.
3. **Check for an SSL/TLS option.** The logger's certificate is dated Sep 2025,
   so this is recent firmware, and newer builds can require TLS on Modbus TCP.
   `internal/modbus` speaks plain TCP. If TLS turns out to be mandatory the
   client needs a TLS dial path; the symptom is 502 opening but every request
   failing at the handshake.

Do not forward 502 to the internet. Modbus has no authentication of any kind and
the VPN already works.

## Verifying the connection

```bash
go build -o essprobe ./cmd/essprobe
./essprobe -addr 10.0.80.91:502 -unit 0 identify
```

Unit 0 is the **logger**, which has its own register map, not the LUNA2000B one.
Expect a good device list and an implausible or failed nameplate check — that is
correct behaviour at unit 0, not a fault. The device list is the point: the
`0x2B`/`0x0E` code-3 query (§4.3.6.2) returns each downstream device with model,
software version, ESN and device ID.

The ESS unit IDs are **5 and 6**, and only in logical-address mode; they come
from **Mantenimiento → Gestión de dispositivos** in the logger UI. The `Net.8.129`
/ `Net.8.130` names in FusionSolar are not unit IDs.

Then, per ESS unit:

```bash
./essprobe -addr 10.0.80.91:502 -unit <id> identify   # RatedCapacity must read 215
./essprobe -addr 10.0.80.91:502 -unit <id> dump
./essprobe -addr 10.0.80.91:502 -unit <id> watch -interval 10s -out ess-<id>.jsonl
```

**`RatedCapacity` (30236) is the single check that validates everything.** It is
a U32 with a gain of 1000; a LUNA2000-215 that decodes to `215.0` confirms the
address space, the word order and the gain at once. If it reads 216, or
millions, the mapping is wrong and nothing else read from the device means
anything.

## What is built

| Path | Contents |
|---|---|
| `internal/modbus/` | Modbus-TCP client: **only** `0x03` and `0x2B`/`0x0E` |
| `internal/modbus/modbussim/` | in-process server for tests and offline work |
| `battery/driver/huawei/registers.go` | all 168 signals of table 3-1, enums, pack helpers |
| `battery/driver/huawei/codec.go` | words ↔ values: gain, sign extension, range checks |
| `battery/driver/huawei/alarms.go` | all 207 alarms of table 3-2, plus decoding |
| `battery/driver/huawei/blocks.go` | batching reads into `0x03` requests |
| `battery/driver/huawei/devicelist.go` | parsing the vendor device-description format |
| `cmd/essprobe/` | read-only field diagnostic |

**No `driver.Driver` implementation yet.** Nothing calls `driver.Register`, there
is no blank import in `cmd/gok/main.go`, and the Web UI driver select is
untouched. What exists is the vendor document turned into checked Go data, plus
a tool to test it against real equipment.

### The read-only guarantee

`internal/modbus` implements no write function code — `0x06` and `0x10` do not
exist anywhere in the package. Nothing that links it can alter equipment state.
That is what makes `essprobe` safe to run against a site in production service.
When the driver needs writes, they go in a separate path, and this property has
to be restated in weaker terms (no call sites rather than no code).

### How the tables were generated

Not transcribed by hand. `pdftotext -layout` on the PDF, then parsed by column
offsets taken from each page's own header row, then the Go literals emitted from
the parsed JSON. The parse self-validates — 168 rows with sequential numbering,
every type/access/gain in a legal set, no duplicate addresses, alarms confined to
the 52 documented alarm words — and every emitted literal was diffed back against
the source rows. Worth redoing the same way if Huawei issues a revision.

Two parsing traps, in case it is repeated: full-width CJK glyphs (`（`) bleed one
character left into the previous column, and rows wrap such that a row number
like 154 arrives as `15` then `4` on consecutive lines.

## Register map essentials

Addresses are **literal Modbus PDU addresses** — the document's own example
(§4.3.3.4) reads 32306 as `0x7E32`, so there is no 40001-style offset. Only
function codes `0x03`, `0x06`, `0x10` are supported (§4.3.1); everything is in
the holding-register space. Port 502 (§4.2.1.4).

Control surface, all of it:

| Register | Signal | Notes |
|---|---|---|
| 42000 | Power on/off | 1 = run, 2 = off (not 0/1) |
| **42915** | **Active power (kW)** | I32, gain 1000 → wire carries watts |
| 42913 | Active power (%) | I16, gain 100, [-100, 100] |
| 42936 | Active power baseline | reference for the % form |
| 42002 | Charge cut-off SOC | **[90, 100]** — much narrower than our `soc_limit` |
| 42003 | Discharge cut-off SOC | **[0, 15]** |
| 42207 | Charging confirmation | write-only |
| 43133 | Working mode | 0 = PQ, 1 = VSG. **Not** a Sonnen auto/manual equivalent |

`42915`'s range is `[RPmax, Pmax]`, which is dynamic — read it at runtime from
33097 and 32853, both of which are in the table. `Register.Encode` therefore
leaves it unbounded, while enforcing the four registers whose bounds are literal.

## Open questions

1. **Sign of 42915 when written.** 30417, 32986 and 42913 are settled (see
   above); 42915 is inferred as negative = charge and needs confirming at the
   first write.
2. **Replace or coexist with the SmartLogger's dispatch.** Settled that it is the
   logger, writing 42913 continuously. Open: which logger setting hands ESS
   control to a third-party master (a remote-dispatch mode in its power/ESS
   control settings), or whether to dispatch at plant level through the logger's
   own register map instead of per cabinet. The latter needs Huawei's
   *SmartLogger Modbus Interface Definitions*, which we do not have yet.
3. **No watchdog register exists.** A setpoint written persists if the writer
   dies. Whatever eventually writes must guarantee ramp-to-zero on shutdown, and
   must sit on the site LAN — never behind a VPN that can drop mid-command.
4. **Concurrent client limit** on the logger, and whether enabling Modbus TCP
   affects the existing FusionSolar link.
5. **TLS on Modbus TCP**, per the configuration section above.

## Bring-up stages

| Stage | Where it runs | Risk |
|---|---|---|
| 0 — build probe and simulator | local | none — **done** |
| 1 — identify, nameplate check, dump | laptop over VPN | one client slot — **done** |
| 2 — observe: polarity, competing master, alarm baseline | Pi on site preferred | one client slot — **polarity and dispatcher done**; multi-day baseline pending |
| 3 — write-path proof: read 42002, write the same value back | laptop over VPN | no behavioural change |
| 4 — first real setpoint | **must be a host on the site LAN** | needs owner sign-off |

Stage 3's trick is that writing a register's own current value proves function
code `0x06` and write permission (no `0x80` exception) while changing nothing.

Stage 4 cannot run over the VPN: with no watchdog register, a tunnel drop
mid-command leaves a production battery pinned at a commanded power with nothing
to unwind it. Stage 2 also wants the Pi on site, so a multi-day recording does
not depend on a VPN session staying up.

## Adding the driver later

Per `CLAUDE.md`, a new driver needs: a package implementing `driver.Driver`, a
`driver.Register("huawei", …)` call in its `init()`, a blank import in
`cmd/gok/main.go`, and a matching option in
`web/ui/src/components/config/BatteryConfigForm.tsx`.

Two frictions to expect. `Driver.SwitchOperatingModeToManual/Auto` is
Sonnen-shaped; Huawei's 43133 is grid-forming vs grid-following, a different
axis entirely, so those become no-ops rather than being forced onto 43133. And
`entity.SystemStatus` is the raw Sonnen JSON DTO — only eight of its fields are
read anywhere (`USOC`, `RSOC`, `RemainingCapacityWh`, `PacTotalW`,
`BatteryCharging`, `BatteryDischarging`, `OperatingMode`, `ConsumptionW`, the
last being telemetry only), so the Huawei mapping is small, but the type will
keep getting more awkward with each vendor added.
