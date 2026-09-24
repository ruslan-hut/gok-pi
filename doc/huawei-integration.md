# Huawei LUNA2000B Integration (Modbus-TCP)

Support for Huawei LUNA2000B C&I energy storage systems, reached over Modbus-TCP.
Status: **reading works on the real site; nothing writes yet.** Modbus TCP is
enabled on the SmartLogger, both ESS units answer, and the cabinet point table is
validated against them (2026-09-15). The SmartLogger's own interface document
arrived on 2026-09-24. It gives a plant-level ESS dispatch register at unit 0, so
dispatch no longer has to go to each cabinet. No driver exists yet.

Source documents, both in `huawei/`:

| Document | Issue | Covers | Cited as |
|---|---|---|---|
| `LUNA2000B ESS Modbus Port Definitions.pdf` | 01 (2025-09-10) | each ESS cabinet (units 5, 6) | §n, *table 3-n* |
| `SmartLogger V300R024C10 ModBus Interface Definitions.pdf` | 54 (2026-02-26) | the logger (unit 0), power meter, device list | **SL** §n, *SL table 2-n* |

The site logger runs V300R024C10SPC161, which is the release that document covers.

```
gok agent ──Modbus-TCP:502──▶ SmartLogger 10.0.80.91 ──▶ unit 5  ESS LUNA2000B-V2
   (master)                    (unit 0)                ──▶ unit 6  ESS LUNA2000B-V2
                                  │                    ──▶ unit 1  Inverter SUN2000-50KTL-M3
                                  │                    ──▶ unit 11 PowerMeter
                                  └─ plant-level ESS dispatch: 40381 / 40383 (SL §3.7, §3.8)
```

## Target site

| | |
|---|---|
| Plant | Electrolinera_Pedernoso (EV charging station) |
| Logger | SmartLogger, serial `102597484606`, `10.0.80.91` |
| ESS | `ESS(Net.8.129)`, `ESS(Net.8.130)`: 2 × 215 kWh = 430.080 kWh nominal |
| Also on the logger | `Inverter(COM1-1)` (50 kW PV), `Meter-AM0010259748…` |
| External | `87.247.129.133:27250` → `10.0.80.91:27250` (NAT rule) |
| Access | VPN, routed; laptop lands on `10.0.80.155` |
| State | In production service, cycling daily |

PV (SUN2000) and storage (LUNA2000B with its own PCS) are separate AC-connected
devices, so the site is **AC-coupled**. That matters because the logger's
separate PV and ESS dispatch ports only work on AC-coupled arrays (SL §3.5–3.10).

Being an EV charging station, this site is a natural fit for the existing
charger↔battery links. See [evsys-integration.md](evsys-integration.md).

## Site bring-up, 2026-09-15

### Logger configuration now in place

Changed in the SmartLogger UI, **Ajustes → Parámetros de comunicación → Modbus TCP**:

- Modbus TCP enabled (it was disabled: `:502` answered with an immediate TCP RST).
- **Modo de dirección: dirección lógica.** In communication-address mode only the
  RS485 devices (inverter 1, meter 11) had unit IDs. The ESS cabinets are
  network-attached and did not answer at any unit ID from 1 to 247. Logical-address
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

Unit numbers for the ESS come from the logger UI. The device list itself reports
device ID 0 for both, as it does for any network-attached device. We have not
established which ESN is unit 5 and which is unit 6.

The logger's device-list responses do three unusual things, all handled in
`internal/modbus`:

- It sets the object count to the size of the whole list but sends only one
  object per response.
- It sends object 0x87 as a binary integer. That matches SL table 4-12, which
  types 0x87 as `int`, but most readers would expect text.
- It pads descriptions beyond the frame limit. The logger document itself puts
  that limit at 256 bytes (SL §4.2.2).

### Point table validated

`essprobe dump -all` on units 5 and 6: **92 of 92 registers read, none failed**,
every value plausible. `RatedCapacity` reads **215.04 kWh** on each, and
2 × 215.04 = 430.08 kWh is exactly the nominal capacity FusionSolar shows.
Pmax / RPmax read ±140.4 kW; charge cut-off 100 %, discharge cut-off 5 %;
working mode PQ; no alarms raised on either unit.

### Sign conventions confirmed

Sampled read-only every 30 s for 4 minutes, midday, both units charging from PV:
control SOC rose 92.0 → 92.6 %, *energy charged today* grew 1.36 kWh (≈ 20.4 kW,
matching the measured power) while *energy discharged today* stayed flat.

| Register | While charging | Convention |
|---|---|---|
| 30417 Charge/Discharge power | +20 kW | battery side: **positive = charge** |
| 32037 Rack current | +24.6 A | positive = charge |
| 32986 Active power | −20.4 kW | AC side: **negative = charge** |
| 42913 Active power (%) | −14.5 % | **negative = charge** |

The two conventions are opposite: 30417 is measured at the battery, while 32986
and the setpoints are measured at the AC terminal. `42913 × 42936 / 100`
reproduces 32986 to within 0.1 kW (−14.58 % × 140.4 kW = −20.47 kW), so the %
setpoint uses the AC-side convention too.

The logger document states the same AC-side convention for its own dispatch
registers: 40383 "a negative value indicates charging", 40420 negative means
"supplied from the power grid", and 40412 gives the maximum charge power "(negative
value)".

### The dispatcher is the SmartLogger, via 42913

On both units `42915` (kW setpoint) reads 0, while `42913` (active power %) holds
the live dispatch and moves between samples (−14.45 … −14.61 %, 30 s apart).
Work status is `0x0204 Running: limited power`. So the SmartLogger runs a
continuous control loop and commands the cabinets through the **percentage**
register against a 140.4 kW baseline, not the kW register. Anything we write to
either cabinet register will be overwritten on the logger's next cycle.

### SmartLogger control configuration (read, not changed)

Read from the logger web UI on 2026-09-15; nothing was submitted.

| Page | Setting | Value |
|---|---|---|
| Power Adjustment → Active Power Control | Active power control mode | **Export Limitation (kW)** |
| | Start control / meter direction / limitation mode | Yes / Positive / Total power |
| | Maximum grid feed-in power | 99 kW |
| | Power lowering adjustment period / protection time / raising threshold | 0.5 s / 3.0 s / 5 kW |
| | PV / PCS power limit upon communication failure | 0 % / 0 % |
| Battery Settings | Working mode | **Maximum self-consumption** |
| | Grid active power threshold during discharge / deadband | 100 W / 35 W |
| | Scheduling mode | Maximize energy |
| | Array end-of-charge / end-of-discharge SOC | 100 % / 5 % |
| Battery Settings → Capacity Control | Peak shaving / power boost limit | No control / No control |
| Microgrid Control | MGCC mode | Disable |
| Comm. Param. → Modbus TCP | Link setting | Enable (Limited): `10.0.80.155`, `10.0.80.154` |
| | Address mode / logger address / fast scheduling | Logical address / 0 / Disable |
| | Communication abnormality detection time | 3.0 s |
| | Protection upon PPC communication error | Disable |
| Status bar | AI control | Disabled |

Feature Parameters and Other Parameters hold nothing dispatch-related.

**What the logger is doing:** holding grid exchange at about zero. PV surplus
charges the ESS and the ESS covers site load. The overview read PV 40.3 kW,
battery −39.3 kW, load 1.1 kW, grid 0.1 kW. 42913 on each cabinet is the output
of that loop.

**Modes the logger offers:**

- Active power control mode: No limit · DI active scheduling · Percentage
  fixed-value limitation (open loop) · **Remote communication scheduling** ·
  Export Limitation (kW) · Remote output control
- Battery working mode: No control · Maximum self-consumption · TOU ·
  **Charge/Discharge based on grid dispatch** · TOU (fixed power) · Custom

According to the working-mode help text, *No control* is for on-site commissioning
only. AI energy management, when enabled, overrides the local working mode, and
power-limited grid connection cannot be combined with TOU fixed power.

## SmartLogger register interface (unit 0)

Received 2026-09-24. All addresses are at **unit 0**, where "the logic device ID
is fixed to 0" (SL §2.1). The encoding matches the cabinet document: literal PDU
addresses, big-endian, and the high word first for 32-bit values. The logger
supports function codes `0x03`, `0x06`, `0x10` and `0x2B` (SL §4.3.2). An RW register
"will be retained until updated the next time" (SL §2).

### ESS dispatch

| Register | Signal | Type, gain | Range / notes |
|---|---|---|---|
| **40381** | **Active ESS power, fixed value** | RW I32, gain 10 → wire in 0.1 kW | ±total rated PCS power (40398). **Out-of-range values are silently not executed.** The logger spreads it by percentage over all PCSs (SL §3.7) |
| 40383 | Active ESS power, percentage | RW I16, gain 10 → 0.1 % | [−100.0, 100.0], **negative = charge**, reference = 40398 (SL §3.8) |
| 40420 | Active power, fixed value (PV + ESS together) | RW I32, gain 10 | negative = from grid. Not needed while ESS and PV have separate ports |
| 40428 | Active power, percentage (PV + ESS) | RW I16, gain 10 | [−100, 100] |
| 40378 / 40380 | PV active power, fixed / % | RW U32 g10 / U16 g10 | PV curtailment. Not ours to touch |
| 40430 | Active power, **highest priority** | RW I32, **gain 1000** | "only for the source control terminal", blocks every other active-power port; `0x7FFFFFFF` releases it; 40578 bit 0 reports it active. **Do not use.** |
| 42470 / 42471 | Array end-of-charge / end-of-discharge SOC | RW U16, gain 1 | [90, 100] / [0, 15]. The UI's 100 % / 5 % |
| 40198 / 40199 | ESS shutdown / startup | WO U16 | value 0 only. Not for normal dispatch |

The sign of **40381** is not written down. Its symmetric range, the explicit
convention of 40383 and 40420, and the cabinets' 42913 all point to negative =
charge, but that stays inferred until the first write.

### Mode, protection and status

| Register | Signal | Notes |
|---|---|---|
| 40737 | Active power control mode (RO) | 0 no restriction · 1 DI · 3 % open loop · **4 remote communication scheduling** · **6 grid connection with limited power (kW)**, i.e. today's Export Limitation · 200 remote output control · 65534 no scheduling |
| **41889** | ActivePowerControlMethod (**RW**) | No enum or range given. Presumably the same values as 40737, which would let us switch the active power control mode over Modbus. Unverified |
| 40738 / 40802 | Active power scheduling target, kW g10 / % | RO readback of what the logger is dispatching |
| 40578 | Status information | bit 0: 40430 override active |
| 41947 / 41948 / 41949 | Shut down array on comm timeout / detection time [60, 1800] s / start up on recovery | RW. A heavy shutdown protection, separate from the 3.0 s UI timer |
| 42454 | PPC communication status | bit 0: 1 = abnormal |
| 50000–50007 | Alarm words 1–8 | SL table 2-2. Alarm 1100 sub-ID 5 (50000 bit 4): no or invalid dispatch commands arriving while in remote communication scheduling |

**Not in the document:** battery working mode, TOU schedule, capacity control
and the "PCS power limit upon communication failure" setting. The last *Working
mode* register (42256) was deleted in issue 45. So a switch of the battery
working mode (self-consumption ↔ grid dispatch) can **only be done in the web
UI**. gok cannot do it.

### Plant-level telemetry

Enough to run a whole-site driver from unit 0 alone:

| Register | Signal | Type, gain | Expected on this site |
|---|---|---|---|
| 40515 / 40527 | SOC (plant) / array actual SOC | U16, gain 10 | ≈ mean of the cabinets' SOC |
| 40516 | SOH | U16, gain 10 | |
| 40484 | Rated ESS capacity | U32, gain 1000, kWh | **430.08**, the unit-0 equivalent of the RatedCapacity check |
| 40480 / 40482 | Chargeable / dischargeable energy | U32, gain 1000, kWh | |
| 40398 | Rated ESS power | U32, gain 1000, kW | 280.8 (2 × 140.4), the base for 40383 |
| 40412 / 40697 | Min / max active power adjustment | I32, gain 10, kW | 40412 = max charge power, negative |
| 40490 / 40492 | Max ESS charge / discharge power now | U32, gain 1000, kW | |
| 40392 | Active ESS power | I32, gain 1000, kW | sign unknown. "Output" suggests AC side, negative = charge |
| 40507 | ESS charge/discharge power | I32, gain 1000, kW | sign unknown. Battery side like 30417? |
| 30014 | ESS active power (fast) | I32, gain 1000, kW | fast interface |
| 40468 / 40470 | Energy charged / discharged today | U32, gain 100, kWh | = sum of the cabinets' daily counters |
| 40488 / 40489 / 40207 | Number of ESSs / PCSs / running PCSs | U16 | 2 / 2 / 2 |
| 40217 / 40218 | End-of-discharge / end-of-charge SOC (RO) | U16, gain 10 | 5.0 / 100.0 |

### Power meter (unit 11)

SL §2.4: **positive = fed to the grid, negative = drawn from the grid**. Key
registers: 32278 active power (I32, gain 1000, kW), 32335/32337/32339 per-phase
active power, 32357 / 32349 positive / negative active energy (I64, gain 100;
which one is import follows the meter direction setting, so check it against
32278 before use). If gok
takes over dispatch, the feed-in cap and self-consumption depend on this meter
(see below).

### Timing

SL §4.3.1 and §4.2.4: control cycle ≥ 200 ms, query cycle ≥ 100 ms, suggested
polling of ESS data every 2 s, response timeout 5 s. gok's 10 s poll is well
inside that.

### Errata in the logger document

These matter if the table is ever machine-extracted like the cabinet one:

- Row 43 repeats 40384. Row 52 repeats 40402, where the change history shows
  it should be 40404.
- 40446 appears twice (rows 71 and 91). The I64 registers 40446/40450/40454/40458
  are listed with quantity 2. An I64 needs 4.
- The DST state enum (40007) lists `1` twice.
- The ESS rows in SL §3.7, §3.8 and §3.10 carry the PV text "the independent PV
  adjustment port supports only arrays with AC coupling". It evidently applies to
  the ESS ports as well.
- Gains are not uniform across related registers: 40420 and 40381 use gain 10,
  while 40430 uses 1000. Encode each one from its own row.

## Handing over control

What the document settles:

- **Dispatch goes to the logger, not to the cabinets.** gok writes 40381 once at
  unit 0 and the logger splits it across both PCSs, still enforcing its own
  SOC limits. The cabinet registers 42913/42915 would be overwritten anyway.
- **The communication-loss fallback lives at the logger.** It uses the
  "Communication abnormality detection time" (3.0 s) and the "PCS power limit upon
  communication failure" (0 %). A dispatch through the logger therefore has a watchdog,
  which a cabinet write lacks. **Not yet established:** whether the timer counts
  any Modbus request or only dispatch writes, and whether it applies outside
  remote communication scheduling.
- **The battery working mode cannot be switched over Modbus.** Any handover
  starts with a manual UI change on site or over VPN.

The site can go one of three ways:

| | A. Full remote dispatch | B. Switch only for gok windows | C. Coexist, no writes |
|---|---|---|---|
| Logger UI (once) | Battery: *grid dispatch*; active power: *remote communication scheduling* | Battery stays *max self-consumption* | Battery: *TOU* or unchanged |
| gok writes | 40381 continuously | 41889 → 4 plus 40381 inside windows; 41889 → 6 afterwards | nothing |
| Self-consumption / 99 kW cap | **gok must re-implement both** from meter 32278 | logger's own, outside windows | logger's own |
| Fits gok's manual/auto model | "auto" does not exist | yes: manual = 4, auto = 6 | n/a |
| Unknown | keepalive semantics | **whether the logger honours 40381 while the battery is in self-consumption mode**, and the 41889 enum | price-driven changes stay manual |

**Recommendation: aim for B and verify it before choosing.** It maps directly
onto `SwitchOperatingModeToManual/Auto` and keeps the logger's proven
self-consumption loop outside gok's windows. The site's EV discharge already
works under that loop. B rests on one fact that only Huawei or a test can supply:
whether 40381 takes effect while the battery is still in *Maximum
self-consumption*. If it does not, choose between A (gok becomes the plant
controller) and C (static TOU in the UI) on business grounds.

Whichever way it goes, while the logger is in remote communication scheduling
its Export Limitation loop is off, so the **99 kW feed-in cap** falls to gok. With
50 kW of PV nameplate, any discharge setpoint ≤ 49 kW keeps export ≤ 99 kW
regardless of load. Above that, limit dynamically from meter 32278. Charging from
the grid has the mirror-image concern: check the contracted import power
(*potencia contratada*) before any charge setpoint above what the connection allows
alongside the EV chargers.

## Network notes

- **The logger is the only path.** `10.0.80.129/.130` answer nothing on the LAN;
  the cabinets sit on the logger's downstream segment and are addressed by
  Modbus unit ID behind it.
- **The Modbus TCP link is whitelisted.** *Enable (Limited)* admits only
  `10.0.80.155` and `10.0.80.154`. A Pi on site needs its address added, or it
  will be refused. SL table 4-4 defines exception `0x80 NO PERMISSION` for
  this kind of failure.
- **Port 27250 is not Modbus.** It is the port the SmartLogger dials out on to
  reach FusionSolar; the NAT rule reaches a closed port. (§4.2.1.4 of the
  cabinet document describes a reverse mode where the device dials out to a
  master; not needed while the VPN works.)
- **Do not forward 502 to the internet.** Modbus has no authentication of any
  kind and the VPN already works.

## Verifying the connection

`essprobe` reads three point tables, selected with `-device`: `ess` for a
cabinet (the default), `logger` for the SmartLogger at unit 0, and `meter` for
the power meter at its RS485 address.

```bash
go build -o essprobe ./cmd/essprobe
./essprobe -addr 10.0.80.91:502 -unit 0 -device logger identify
```

At unit 0, `identify` returns the device list and the logger nameplate check.
The device list comes from the `0x2B`/`0x0E` code-3 query (§4.3.6.2, SL §4.3.7.2)
and gives each downstream device's model, software version, ESN and device ID. The
decisive value in the nameplate check is **40484 = 430.08 kWh**, the sum of the
cabinets' RatedCapacity. It confirms address space, word order and gain for unit 0
in the same way RatedCapacity does for a cabinet. The check also shows the active
power control mode (40737, expected 6), 41889 next to it, what the logger is
dispatching now (40738), and the array SOC limits.

The ESS unit IDs are **5 and 6**, and only in logical-address mode; they come
from **Mantenimiento → Gestión de dispositivos** in the logger UI. The `Net.8.129`
/ `Net.8.130` names in FusionSolar are not unit IDs.

Per ESS unit:

```bash
./essprobe -addr 10.0.80.91:502 -unit <id> identify   # RatedCapacity must read 215
./essprobe -addr 10.0.80.91:502 -unit <id> dump
./essprobe -addr 10.0.80.91:502 -unit <id> watch -interval 10s -out ess-<id>.jsonl
```

**`RatedCapacity` (30236) is the single check that validates everything.** It is
a U32 with a gain of 1000. On a LUNA2000-215, a reading of `215.0` confirms the
address space, the word order and the gain at once. If it reads 216, or millions,
the mapping is wrong and nothing else read from the device means anything.

The logger and the meter (stage 2b):

```bash
./essprobe -addr 10.0.80.91:502 -unit 0  -device logger dump -all
./essprobe -addr 10.0.80.91:502 -unit 0  -device logger alarms
./essprobe -addr 10.0.80.91:502 -unit 0  -device logger watch -interval 10s -out logger.jsonl
./essprobe -addr 10.0.80.91:502 -unit 11 -device meter identify   # voltages ≈ 230 / 400 V
./essprobe -addr 10.0.80.91:502 -unit 11 -device meter dump
```

`watch -device logger` correlates the sign of 40507, 40392 and 30014 with the
direction the plant SOC (40515) moves, and prints a verdict for each one on exit.
Run it while the battery is cycling, as was done for the cabinets. That settles
open question 4 without writing anything. Any change to 40381, 40383, 41889 or the
other logger setpoints is reported as "ANOTHER MASTER WROTE".

If the logger refuses a batched read with `0x02` (a gap between documented
registers), `essprobe` re-reads that block one register at a time. The dump then
shows exactly which address is unsupported, instead of failing the whole block.

## What is built

| Path | Contents |
|---|---|
| `internal/modbus/` | Modbus-TCP client: **only** `0x03` and `0x2B`/`0x0E` |
| `internal/modbus/modbussim/` | in-process server for tests and offline work |
| `battery/driver/huawei/registers.go` | all 168 signals of the cabinet table 3-1, enums, pack helpers |
| `battery/driver/huawei/codec.go` | words ↔ values: gain, sign extension (up to I64), range checks |
| `battery/driver/huawei/alarms.go` | all 207 alarms of the cabinet table 3-2, plus decoding |
| `battery/driver/huawei/smartlogger.go` | 70 logger registers of SL table 2-1 (dispatch, mode, protection, plant telemetry) and 22 meter registers of SL table 2-5 |
| `battery/driver/huawei/smartlogger_alarms.go` | all 90 alarms of SL table 2-2, with sub-IDs and causes |
| `battery/driver/huawei/blocks.go` | batching reads into `0x03` requests |
| `battery/driver/huawei/devicelist.go` | parsing the vendor device-description format |
| `cmd/essprobe/` | read-only field diagnostic for cabinet, logger and meter |

The SmartLogger tables were checked mechanically against the PDF. Every
register's address, type, access and gain, and every alarm's ID, sub-ID, word and
bit, match the extracted rows. The logger table covers the rows relevant to ESS
dispatch and plant telemetry. It leaves out PV-only statistics, black start, IV
scanning, Japanese remote-output signals and the write-only commands. The two
alarm bits the document assigns twice (50005 bits 13 and 14) are kept with both
claimants, so a raised bit reports both.

**No `driver.Driver` implementation yet.** Nothing calls `driver.Register`, there
is no blank import in `cmd/gok/main.go`, and the Web UI driver select is
untouched.

### The read-only guarantee

`internal/modbus` implements no write function code: `0x06` and `0x10` do not
exist anywhere in the package, so nothing that links it can alter equipment
state. That is what makes `essprobe` safe to run against a site in production
service. When the driver needs writes, they go in a separate path, and this
property then has to be restated in weaker terms (no call sites rather than no
code). 40381 is an I32, so that path needs `0x10`. `0x06` alone cannot write it.

### How the tables were generated

Not transcribed by hand (`huawei/regen_tables.py`). `pdftotext -layout` on the PDF, then parsed by column
offsets taken from each page's own header row, then the Go literals emitted from
the parsed JSON. The parse self-validates: 168 rows with sequential numbering,
every type/access/gain in a legal set, no duplicate addresses, alarms confined to
the 52 documented alarm words. Every emitted literal was diffed back against
the source rows. Worth redoing the same way if Huawei issues a revision.

Two parsing traps, in case it is repeated: full-width CJK glyphs (`（`) bleed one
character left into the previous column, and rows wrap such that a row number
like 154 arrives as `15` then `4` on consecutive lines.

The SmartLogger tables were written by hand as a subset rather than
generated, because the errata (above) break the "no duplicate addresses" and "type
matches quantity" checks. They were then diffed against rows extracted with
`pdftotext -layout`, which found no mismatches. Regenerating the whole of SL table
2-1 would need explicit overrides for those errata, not loosened checks.

## Cabinet register map essentials (units 5, 6)

Addresses are **literal Modbus PDU addresses**. The document's own example
(§4.3.3.4) reads 32306 as `0x7E32`, so there is no 40001-style offset. Only
function codes `0x03`, `0x06`, `0x10` are supported (§4.3.1); everything is in
the holding-register space. Port 502 (§4.2.1.4).

Cabinet control surface. These registers are **no longer the planned write
path**, since the logger overwrites them and they have no watchdog:

| Register | Signal | Notes |
|---|---|---|
| 42000 | Power on/off | 1 = run, 2 = off (not 0/1) |
| 42915 | Active power (kW) | I32, gain 1000 → wire carries watts |
| 42913 | Active power (%) | I16, gain 100, [-100, 100]. The logger writes this one |
| 42936 | Active power baseline | reference for the % form |
| 42002 | Charge cut-off SOC | **[90, 100]**, much narrower than our `soc_limit` |
| 42003 | Discharge cut-off SOC | **[0, 15]** |
| 42207 | Charging confirmation | write-only |
| 43133 | Working mode | 0 = PQ, 1 = VSG. **Not** a Sonnen auto/manual equivalent |

`42915`'s range is `[RPmax, Pmax]`, which is dynamic, so it has to be read at
runtime from 33097 and 32853, both of which are in the table.
`Register.Encode` therefore leaves it unbounded, while enforcing the four registers whose bounds are literal.

The cabinets stay the source of per-rack detail: temperatures, rack current,
alarms (table 3-2), and working status.

## Open questions

1. **Does 40381 act while the battery working mode is *Maximum
   self-consumption*?** This decides between options B and A/C. Ask Huawei or the
   installer first. Otherwise test it in stage 4.
2. **The 41889 enum.** Read it next to 40737 (expect both = 6 today). Then
   confirm with Huawei that writing 4 / 6 switches the mode the way the UI does.
3. **Keepalive semantics of the 3.0 s timer.** Does it count any request, or only
   dispatch writes? gok's controller sends a setpoint only when it changes, so a
   driver on this path needs its own refresh loop (≤ 1 s) either way. The
   fallback must also be seen to work (stop writing → ESS power to 0 within about
   3 s) before any unattended use.
4. **Sign of 40381, 40392 and 40507.** 40381 is inferred as negative = charge.
   40392 and 40507 are read-only and can be settled in stage 2b against 30417 and
   32986.
5. **Grid limits** if dispatch is taken over: the 99 kW feed-in cap (a static
   ≤ 49 kW discharge cap covers it) and the contracted import power for grid
   charging.
6. **Concurrent client limit** on the logger, and whether Modbus TCP traffic
   affects the FusionSolar link. The logger document is silent on both.

## Bring-up stages

| Stage | Where it runs | Risk |
|---|---|---|
| 0: build probe and simulator | local | none. **Done** |
| 1: identify, nameplate check, dump (cabinets) | laptop over VPN | one client slot. **Done** |
| 2: observe cabinets: polarity, competing master, alarm baseline | Pi on site preferred | one client slot. **Polarity and dispatcher done**; multi-day baseline pending |
| 2b: logger and meter: `identify`, `dump -all`, `alarms`, then `watch -device logger` while cycling | laptop over VPN | read-only. **Tooling ready; next on site** |
| 3: write-path proof at unit 0: read 42470 and write the same value back, once with `0x06` and once with `0x10` | laptop over VPN | no behavioural change |
| 4: logger handover (UI change) and first 40381 setpoint, **including the communication-loss test** | see below | needs owner sign-off and a check of the grid limits |

Stage 2b's single decisive check is **40484 = 430.08 kWh**, the unit-0
counterpart of the cabinets' RatedCapacity check. 40737 = 6 and 42470/42471 =
100/5 confirm that the registers match what the UI shows.

Stage 3 writes a register's own current value. That proves the function code and
write permission (no `0x80`/`0x01` exception) while changing nothing. 42470 is
the best choice: it is an ordinary RW parameter with a narrow range, and it is
not a dispatch input.

Stage 4 no longer strictly requires a host on the site LAN, because the logger's
comm-loss limit unwinds a stranded setpoint. That holds only after the fallback
has been seen to work in the same session: stop writing and watch 30014/40392 go
to 0. Until then, and for production in any case, run it from a Pi on the site
LAN (added to the Modbus TCP whitelist) so that a multi-day recording and a live
setpoint do not depend on a VPN session. Start with a small discharge
(≤ 10 kW), in a window with no EV sessions.

## Adding the driver later

Per [architecture.md](architecture.md#battery-drivers), a new driver needs: a package implementing `driver.Driver`, a
`driver.Register("huawei", …)` call in its `init()`, a blank import in
`cmd/gok/main.go`, and a matching option in
`web/ui/src/components/config/BatteryConfigForm.tsx`.

With the logger document, the mapping becomes a **whole-site driver at unit 0**:
one gok battery = the ESS array behind one SmartLogger.

| `driver.Driver` | Huawei (option B) |
|---|---|
| `Status()` | 40515 SOC, 40482 → `RemainingCapacityWh`, 40392 → `PacTotalW` (after the sign check), meter 32278 for grid / consumption |
| `StartDischarge(p)` / `StartCharge(p)` | 40381 = +p / −p in 0.1 kW, capped by 40697 / 40412 and the grid limits; read 40738 back, since out-of-range writes are dropped silently |
| `StopDischarge()` / `StopCharge()` | 40381 = 0 |
| `SwitchOperatingModeToManual` / `…ToAuto` | 41889 = 4 / 6, if open question 2 confirms it; otherwise no-ops |

Under option A there is no "auto" to return to, so the driver would have to run
its own self-consumption loop from meter 32278. That is a much larger piece of
work, and the main reason B is preferred.

Two frictions remain:

- The driver needs a background refresh of 40381 (open question 3). It must also
  write 0 on shutdown, even though the logger fallback covers crashes.
- `entity.SystemStatus` is the raw Sonnen JSON DTO. Only eight of its fields are
  read anywhere (`USOC`, `RSOC`, `RemainingCapacityWh`, `PacTotalW`,
  `BatteryCharging`, `BatteryDischarging`, `OperatingMode`, `ConsumptionW`, the
  last being telemetry only), so the Huawei mapping is small. Still, the type will
  keep getting more awkward with each vendor added.
