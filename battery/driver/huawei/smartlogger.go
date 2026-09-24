package huawei

import "fmt"

// SmartLogger registers, transcribed from "SmartLogger V300R024C10 ModBus
// Interface Definitions", issue 54 (2026-02-26). Row numbers in the doc
// comments refer to the No. column of its table 2-1 (the logger, unit 0) or
// table 2-5 (the power meter, at its RS485 address), so a value can be checked
// against the source.
//
// Only the rows that matter to ESS dispatch and plant telemetry are here, about
// a quarter of table 2-1. PV-only statistics, Japanese remote-output signals,
// black start, IV scanning and the like are left out. So are the write-only
// commands (40198/40199 ESS shutdown and startup and similar): nothing in this
// package writes, and a write-only register cannot be read.
//
// Addressing and encoding match the cabinet table: literal PDU addresses,
// big-endian, high word first (SL section 4.2.3). The logger answers as unit 0
// (SL section 2.1).
//
// Errata in issue 54 that affect this file are noted where they apply. The
// full list is in doc/huawei-integration.md.

// Plant-level telemetry of table 2-1.
var (
	// LoggerActivePowerFast is row 3, "Active power (fast)" at 30010: total
	// output active power of the array, fast interface.
	LoggerActivePowerFast = Register{Name: "Active power (fast)", Addr: 30010, Kind: I32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerPVActivePowerFast is row 5, "PV active power (fast)" at 30012.
	LoggerPVActivePowerFast = Register{Name: "PV active power (fast)", Addr: 30012, Kind: U32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerESSActivePowerFast is row 4, "Energy storage active power (fast)"
	// at 30014: output active power of the array's energy storage. Sign not
	// yet confirmed on site.
	LoggerESSActivePowerFast = Register{Name: "ESS active power (fast)", Addr: 30014, Kind: I32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerEnergyChargedThisMonth is row 6 at 30050.
	LoggerEnergyChargedThisMonth = Register{Name: "Energy charged this month", Addr: 30050, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// LoggerEnergyDischargedThisMonth is row 7 at 30052.
	LoggerEnergyDischargedThisMonth = Register{Name: "Energy discharged this month", Addr: 30052, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// LoggerEnergyChargedThisYear is row 8 at 30054.
	LoggerEnergyChargedThisYear = Register{Name: "Energy charged this year", Addr: 30054, Kind: I64, Gain: 100, Unit: "kWh", Access: RO}

	// LoggerEnergyDischargedThisYear is row 9 at 30058.
	LoggerEnergyDischargedThisYear = Register{Name: "Energy discharged this year", Addr: 30058, Kind: I64, Gain: 100, Unit: "kWh", Access: RO}

	// LoggerExportedToday is row 10, "Power supply to grid today" at 30062.
	LoggerExportedToday = Register{Name: "Energy fed to grid today", Addr: 30062, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// LoggerTotalExported is row 13, "Total power supply to grid" at 30068.
	LoggerTotalExported = Register{Name: "Total energy fed to grid", Addr: 30068, Kind: I64, Gain: 100, Unit: "kWh", Access: RO}

	// LoggerTimeZone is row 17, "Time Zone" at 40005, in seconds.
	LoggerTimeZone = Register{Name: "Time zone offset", Addr: 40005, Kind: I32, Gain: 1, Unit: "s", Access: RO}

	// LoggerLocalTime is row 20, "Local time" at 40009, epoch seconds.
	LoggerLocalTime = Register{Name: "Local time", Addr: 40009, Kind: U32, Gain: 1, Unit: "", Access: RO}

	// LoggerRunningPCSs is row 32, "Quantity of running ESS PCSs" at 40207.
	LoggerRunningPCSs = Register{Name: "Running ESS PCSs", Addr: 40207, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// LoggerEndOfDischargeSOC is row 33, "ESS end-of-discharge SOC" at 40217,
	// derived from the racks' own limits.
	LoggerEndOfDischargeSOC = Register{Name: "ESS end-of-discharge SOC", Addr: 40217, Kind: U16, Gain: 10, Unit: "%", Access: RO}

	// LoggerEndOfChargeSOC is row 34, "ESS end-of-charge SOC" at 40218.
	LoggerEndOfChargeSOC = Register{Name: "ESS end-of-charge SOC", Addr: 40218, Kind: U16, Gain: 10, Unit: "%", Access: RO}

	// LoggerPVActivePower is row 44, "Active PV power" at 40388.
	LoggerPVActivePower = Register{Name: "Active PV power", Addr: 40388, Kind: U32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerESSActivePower is row 46, "Active ESS power" at 40392: actual
	// active output power of the ESS devices. Sign not yet confirmed on site.
	LoggerESSActivePower = Register{Name: "Active ESS power", Addr: 40392, Kind: I32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerRatedPVPower is row 48, "Rated PV power" at 40396.
	LoggerRatedPVPower = Register{Name: "Rated PV power", Addr: 40396, Kind: U32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerRatedESSPower is row 49, "Rated ESS power" at 40398: the reference
	// for LoggerESSActivePowerPercent and the bound of LoggerESSActivePowerSetpoint.
	LoggerRatedESSPower = Register{Name: "Rated ESS power", Addr: 40398, Kind: U32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerMinActivePowerAdjustment is row 56, "Minimum active power adjustment
	// value" at 40412: the maximum charge power of the array's ESS, as a
	// negative value.
	LoggerMinActivePowerAdjustment = Register{Name: "Minimum active power adjustment", Addr: 40412, Kind: I32, Gain: 10, Unit: "kW", Access: RO}

	// LoggerEnergyChargedToday is row 76 at 40468.
	LoggerEnergyChargedToday = Register{Name: "Energy charged today", Addr: 40468, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// LoggerEnergyDischargedToday is row 77 at 40470.
	LoggerEnergyDischargedToday = Register{Name: "Energy discharged today", Addr: 40470, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// LoggerTotalEnergyCharged is row 78 at 40472.
	LoggerTotalEnergyCharged = Register{Name: "Total energy charged", Addr: 40472, Kind: I64, Gain: 100, Unit: "kWh", Access: RO}

	// LoggerTotalEnergyDischarged is row 79 at 40476.
	LoggerTotalEnergyDischarged = Register{Name: "Total energy discharged", Addr: 40476, Kind: I64, Gain: 100, Unit: "kWh", Access: RO}

	// LoggerChargeableCapacity is row 80, "Chargeable capacity" at 40480.
	LoggerChargeableCapacity = Register{Name: "Chargeable capacity", Addr: 40480, Kind: U32, Gain: 1000, Unit: "kWh", Access: RO}

	// LoggerDischargeableCapacity is row 81, "Dischargeable capacity" at 40482.
	LoggerDischargeableCapacity = Register{Name: "Dischargeable capacity", Addr: 40482, Kind: U32, Gain: 1000, Unit: "kWh", Access: RO}

	// LoggerRatedESSCapacity is row 82, "Rated ESS capacity" at 40484: the sum
	// of every cabinet's RatedCapacity, so it is the unit-0 nameplate check.
	LoggerRatedESSCapacity = Register{Name: "Rated ESS capacity", Addr: 40484, Kind: U32, Gain: 1000, Unit: "kWh", Access: RO}

	// LoggerNumberOfESSs is row 84, "Number of ESSs" at 40488.
	LoggerNumberOfESSs = Register{Name: "Number of ESSs", Addr: 40488, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// LoggerNumberOfPCSs is row 85, "Number of PCSs" at 40489.
	LoggerNumberOfPCSs = Register{Name: "Number of PCSs", Addr: 40489, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// LoggerMaxChargePower is row 86, "Maximum ESS charge power" at 40490.
	LoggerMaxChargePower = Register{Name: "Maximum ESS charge power", Addr: 40490, Kind: U32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerMaxDischargePower is row 87, "Maximum ESS discharge power" at 40492.
	LoggerMaxDischargePower = Register{Name: "Maximum ESS discharge power", Addr: 40492, Kind: U32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerStableChargePower is row 88, "Highest stable charge power of ESS" at 40494.
	LoggerStableChargePower = Register{Name: "Highest stable charge power", Addr: 40494, Kind: U32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerStableDischargePower is row 89, "Highest stable discharge power of ESS" at 40496.
	LoggerStableDischargePower = Register{Name: "Highest stable discharge power", Addr: 40496, Kind: U32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerPVPower is row 109, "PV power generation" at 40501.
	LoggerPVPower = Register{Name: "PV power generation", Addr: 40501, Kind: U32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerESSChargeDischargePower is row 92, "Energy storage charge and
	// discharge power" at 40507: total over all batteries. Sign not yet
	// confirmed on site.
	LoggerESSChargeDischargePower = Register{Name: "ESS charge/discharge power", Addr: 40507, Kind: I32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerLoadEnergyToday is row 93, "Current-day power consumption" at
	// 40509: the plant's load today.
	LoggerLoadEnergyToday = Register{Name: "Load energy today", Addr: 40509, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// LoggerSoldToday is row 94, "Electricity sales volume of the day" at
	// 40511: fed into the grid at the connection point today.
	LoggerSoldToday = Register{Name: "Grid export today", Addr: 40511, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// LoggerPurchasedToday is row 95, "Electricity purchased on the current
	// day" at 40513: drawn from the grid at the connection point today.
	LoggerPurchasedToday = Register{Name: "Grid import today", Addr: 40513, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// LoggerSOC is row 96, "SOC" at 40515: plant-level aggregate.
	LoggerSOC = Register{Name: "SOC", Addr: 40515, Kind: U16, Gain: 10, Unit: "%", Access: RO}

	// LoggerSOH is row 97, "SOH" at 40516.
	LoggerSOH = Register{Name: "SOH", Addr: 40516, Kind: U16, Gain: 10, Unit: "%", Access: RO}

	// LoggerSOE is row 98, "SOE" at 40517.
	LoggerSOE = Register{Name: "SOE", Addr: 40517, Kind: U16, Gain: 10, Unit: "%", Access: RO}

	// LoggerActivePower is row 102, "Active power" at 40525: total output of
	// all inverters and PCSs.
	LoggerActivePower = Register{Name: "Active power", Addr: 40525, Kind: I32, Gain: 1000, Unit: "kW", Access: RO}

	// LoggerArraySOC is row 103, "Array actual SOC" at 40527.
	LoggerArraySOC = Register{Name: "Array actual SOC", Addr: 40527, Kind: U16, Gain: 10, Unit: "%", Access: RO}

	// LoggerSteadyChargeableCapacity is row 104 at 40528: chargeable capacity
	// of the clusters whose PCS is not faulted.
	LoggerSteadyChargeableCapacity = Register{Name: "Steady-state chargeable capacity", Addr: 40528, Kind: U32, Gain: 1000, Unit: "kWh", Access: RO}

	// LoggerSteadyDischargeableCapacity is row 105 at 40530.
	LoggerSteadyDischargeableCapacity = Register{Name: "Steady-state dischargeable capacity", Addr: 40530, Kind: U32, Gain: 1000, Unit: "kWh", Access: RO}

	// LoggerPowerFactor is row 106, "Power factor" at 40532.
	LoggerPowerFactor = Register{Name: "Power factor", Addr: 40532, Kind: I16, Gain: 1000, Unit: "", Access: RO}

	// LoggerArrayInOperation is row 107 at 40535: 1 while any inverter or PCS runs.
	LoggerArrayInOperation = Register{Name: "Array in operation", Addr: 40535, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// LoggerArrayShutDown is row 108 at 40536: 1 only when everything is down.
	LoggerArrayShutDown = Register{Name: "Array shut down", Addr: 40536, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// LoggerPCSInOperation is row 111, "ESS PCS in operation" at 40539.
	LoggerPCSInOperation = Register{Name: "ESS PCS in operation", Addr: 40539, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// LoggerPCSShutDown is row 112, "ESS PCS shut down" at 40540.
	LoggerPCSShutDown = Register{Name: "ESS PCS shut down", Addr: 40540, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// LoggerReactivePower is row 116, "Reactive power" at 40544.
	LoggerReactivePower = Register{Name: "Reactive power", Addr: 40544, Kind: I32, Gain: 1000, Unit: "kVar", Access: RO}

	// LoggerStatus is row 131, "Status information" at 40578. Bit 0 is set
	// while LoggerHighestPriorityActivePower overrides everything else.
	LoggerStatus = Register{Name: "Status information", Addr: 40578, Kind: Bits16, Gain: 1, Unit: "", Access: RO}

	// LoggerMaxActivePowerAdjustment is row 143, "Maximum active power
	// adjustment value" at 40697.
	LoggerMaxActivePowerAdjustment = Register{Name: "Maximum active power adjustment", Addr: 40697, Kind: I32, Gain: 10, Unit: "kW", Access: RO}

	// LoggerActivePowerControlMode is row 150, "Active power control mode" at
	// 40737. Values are the ControlMode* constants.
	LoggerActivePowerControlMode = Register{Name: "Active power control mode", Addr: 40737, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// LoggerSchedulingTarget is row 151, "Active power scheduling target
	// value" at 40738: what the logger is dispatching now.
	LoggerSchedulingTarget = Register{Name: "Active power scheduling target", Addr: 40738, Kind: I32, Gain: 10, Unit: "kW", Access: RO}

	// LoggerSchedulingPercent is row 155, "Active power scheduling in
	// percentage" at 40802.
	// Scope: [-100, 100]
	LoggerSchedulingPercent = Register{Name: "Active power scheduling (%)", Addr: 40802, Kind: I32, Gain: 1, Unit: "%", Access: RO}

	// LoggerPPCCommStatus is row 173, "PPC communication connection status"
	// at 42454. Bit 0 set means abnormal.
	LoggerPPCCommStatus = Register{Name: "PPC communication status", Addr: 42454, Kind: Bits16, Gain: 1, Unit: "", Access: RO}

	// LoggerPCSWorkingModeStatus is row 189, "PV array PCS working mode status"
	// at 44364: 0 PQ, 1 VSG, 2 and 3 mixed, 65535 unknown.
	LoggerPCSWorkingModeStatus = Register{Name: "PCS working mode status", Addr: 44364, Kind: U16, Gain: 1, Unit: "", Access: RO}
)

// Writable registers of table 2-1. Nothing in this package writes them; they
// are readable, and polling them shows who else is dispatching the array.
var (
	// LoggerPVActivePowerSetpoint is row 37, "Active PV power adjustment in
	// fixed value" at 40378. Scope: [0, total rated PV inverter power], so it
	// is left unbounded.
	LoggerPVActivePowerSetpoint = Register{Name: "PV active power setpoint (kW)", Addr: 40378, Kind: U32, Gain: 10, Unit: "kW", Access: RW}

	// LoggerPVActivePowerPercent is row 38, "Active PV power adjustment in
	// percentage" at 40380.
	LoggerPVActivePowerPercent = Register{Name: "PV active power setpoint (%)", Addr: 40380, Kind: U16, Gain: 10, Unit: "%", Access: RW, Min: 0, Max: 100, Bounded: true}

	// LoggerESSActivePowerSetpoint is row 39, "Active ESS power adjustment in
	// fixed value" at 40381: the plant-level ESS dispatch. The logger spreads it
	// over every PCS (SL section 3.7). Scope: [-rated PCS power, +rated PCS
	// power], i.e. ±LoggerRatedESSPower, so it is left unbounded here. Values
	// outside the scope are not executed and raise no exception. The sign is
	// inferred to be negative = charge (as for 40383) but not yet confirmed.
	LoggerESSActivePowerSetpoint = Register{Name: "ESS active power setpoint (kW)", Addr: 40381, Kind: I32, Gain: 10, Unit: "kW", Access: RW}

	// LoggerESSActivePowerPercent is row 40, "Active ESS power adjustment in
	// percentage" at 40383. Negative means charging; the reference is
	// LoggerRatedESSPower (SL section 3.8).
	LoggerESSActivePowerPercent = Register{Name: "ESS active power setpoint (%)", Addr: 40383, Kind: I16, Gain: 10, Unit: "%", Access: RW, Min: -100, Max: 100, Bounded: true}

	// LoggerActivePowerSetpoint is row 58, "Active power adjustment" at 40420:
	// PV and ESS together. Negative means drawn from the grid. Dynamic scope.
	LoggerActivePowerSetpoint = Register{Name: "Active power setpoint (kW)", Addr: 40420, Kind: I32, Gain: 10, Unit: "kW", Access: RW}

	// LoggerActivePowerPercent is row 62, "Active power adjustment in
	// percentage" at 40428: PV and ESS together.
	LoggerActivePowerPercent = Register{Name: "Active power setpoint (%)", Addr: 40428, Kind: I16, Gain: 10, Unit: "%", Access: RW, Min: -100, Max: 100, Bounded: true}

	// LoggerHighestPriorityActivePower is row 64, "Active power adjustment
	// (highest-priority)" at 40430. Note gain 1000 where 40420 has 10. Reserved
	// for "the source control terminal": while set it blocks every other
	// active-power port, and ReleaseHighestPriority clears it (SL section 3.11).
	LoggerHighestPriorityActivePower = Register{Name: "Active power setpoint (highest priority)", Addr: 40430, Kind: I32, Gain: 1000, Unit: "kW", Access: RW}

	// LoggerActivePowerControlMethod is row 157, "ActivePowerControlMethod" at
	// 41889. The document gives no enumeration. It presumably takes the
	// ControlMode* values of 40737, which would make the control mode
	// switchable over Modbus; that is unconfirmed.
	LoggerActivePowerControlMethod = Register{Name: "Active power control method", Addr: 41889, Kind: U16, Gain: 1, Unit: "", Access: RW}

	// LoggerShutdownOnCommTimeout is row 163, "Shut down array upon
	// communication timeout" at 41947: 0 disabled, 1 enabled.
	LoggerShutdownOnCommTimeout = Register{Name: "Shut down array on comm timeout", Addr: 41947, Kind: U16, Gain: 1, Unit: "", Access: RW, Min: 0, Max: 1, Bounded: true}

	// LoggerCommTimeout is row 164, "Time for communication exception
	// detection" at 41948, for LoggerShutdownOnCommTimeout. It is not the 3.0 s
	// timer in the Modbus TCP settings, whose range is far shorter.
	LoggerCommTimeout = Register{Name: "Comm exception detection time", Addr: 41948, Kind: U16, Gain: 1, Unit: "s", Access: RW, Min: 60, Max: 1800, Bounded: true}

	// LoggerStartupOnCommRecovery is row 165, "Start up array upon
	// communication recovery" at 41949: 0 disabled, 1 enabled.
	LoggerStartupOnCommRecovery = Register{Name: "Start up array on comm recovery", Addr: 41949, Kind: U16, Gain: 1, Unit: "", Access: RW, Min: 0, Max: 1, Bounded: true}

	// LoggerArrayChargeEndSOC is row 178, "Array charge end SOC" at 42470.
	LoggerArrayChargeEndSOC = Register{Name: "Array charge end SOC", Addr: 42470, Kind: U16, Gain: 1, Unit: "%", Access: RW, Min: 90, Max: 100, Bounded: true}

	// LoggerArrayDischargeEndSOC is row 179, "Array discharge end SOC" at 42471.
	LoggerArrayDischargeEndSOC = Register{Name: "Array discharge end SOC", Addr: 42471, Kind: U16, Gain: 1, Unit: "%", Access: RW, Min: 0, Max: 15, Bounded: true}
)

// ReleaseHighestPriority is the raw value that switches
// LoggerHighestPriorityActivePower off (SL section 3.11).
const ReleaseHighestPriority int64 = 0x7FFFFFFF

// Values of LoggerActivePowerControlMode (row 150). The UI calls 6 "Export
// Limitation (kW)".
const (
	ControlModeNoLimit      uint16 = 0
	ControlModeDI           uint16 = 1
	ControlModePercentOpen  uint16 = 3
	ControlModeRemote       uint16 = 4
	ControlModeLimitedPower uint16 = 6
	ControlModeRemoteOutput uint16 = 200
	ControlModeSlaveLogger  uint16 = 65533
	ControlModeNoScheduling uint16 = 65534
)

// ControlModeName returns the documented name of a LoggerActivePowerControlMode
// value, or a numeric rendering for one the table does not define.
func ControlModeName(v uint16) string {
	if s, ok := controlModeNames[v]; ok {
		return s
	}

	return fmt.Sprintf("unknown(%d)", v)
}

var controlModeNames = map[uint16]string{
	ControlModeNoLimit:      "no restriction",
	ControlModeDI:           "DI active scheduling",
	ControlModePercentOpen:  "percentage limit (open loop)",
	ControlModeRemote:       "remote communication scheduling",
	ControlModeLimitedPower: "grid connection with limited power (kW)",
	ControlModeRemoteOutput: "remote output control",
	ControlModeSlaveLogger:  "slave SmartLogger",
	ControlModeNoScheduling: "no scheduling",
}

// LoggerAlarmWords holds rows 198-205, "Alarm 1" to "Alarm 8" at 50000-50007.
// Their bits are LoggerAlarms.
var LoggerAlarmWords = [8]Register{
	{Name: "Alarm 1", Addr: 50000, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Alarm 2", Addr: 50001, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Alarm 3", Addr: 50002, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Alarm 4", Addr: 50003, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Alarm 5", Addr: 50004, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Alarm 6", Addr: 50005, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Alarm 7", Addr: 50006, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Alarm 8", Addr: 50007, Kind: Bits16, Gain: 1, Access: RO},
}

// LoggerTelemetryRegisters are the plant-level signals worth sampling on every
// poll: SOC and the three candidate power readings, the capacity and
// capability limits, and what the logger is dispatching.
var LoggerTelemetryRegisters = []Register{
	LoggerSOC, LoggerArraySOC, LoggerSOH, LoggerSOE,
	LoggerESSChargeDischargePower, LoggerESSActivePower, LoggerESSActivePowerFast,
	LoggerActivePower, LoggerActivePowerFast, LoggerPVActivePower,
	LoggerChargeableCapacity, LoggerDischargeableCapacity,
	LoggerRatedESSCapacity, LoggerRatedESSPower, LoggerNumberOfESSs, LoggerNumberOfPCSs, LoggerRunningPCSs,
	LoggerMaxChargePower, LoggerMaxDischargePower,
	LoggerMinActivePowerAdjustment, LoggerMaxActivePowerAdjustment,
	LoggerActivePowerControlMode, LoggerSchedulingTarget, LoggerSchedulingPercent,
	LoggerStatus, LoggerPPCCommStatus,
	LoggerEnergyChargedToday, LoggerEnergyDischargedToday,
	LoggerSoldToday, LoggerPurchasedToday,
	LoggerEndOfDischargeSOC, LoggerEndOfChargeSOC,
	LoggerLocalTime,
}

// LoggerSetpointRegisters are the logger's readable, writable registers. A
// change in any of them came from another master: essprobe cannot write.
var LoggerSetpointRegisters = []Register{
	LoggerESSActivePowerSetpoint, LoggerESSActivePowerPercent,
	LoggerActivePowerSetpoint, LoggerActivePowerPercent,
	LoggerHighestPriorityActivePower,
	LoggerPVActivePowerSetpoint, LoggerPVActivePowerPercent,
	LoggerActivePowerControlMethod,
	LoggerShutdownOnCommTimeout, LoggerCommTimeout, LoggerStartupOnCommRecovery,
	LoggerArrayChargeEndSOC, LoggerArrayDischargeEndSOC,
}

// LoggerObservationRegisters is what a passive observer of the logger should
// sample: telemetry, setpoints and the alarm words.
func LoggerObservationRegisters() []Register {
	out := make([]Register, 0, len(LoggerTelemetryRegisters)+len(LoggerSetpointRegisters)+len(LoggerAlarmWords))
	out = append(out, LoggerTelemetryRegisters...)
	out = append(out, LoggerSetpointRegisters...)
	out = append(out, LoggerAlarmWords[:]...)

	return out
}

// LoggerAllRegisters is every logger register this package knows.
func LoggerAllRegisters() []Register {
	return append(LoggerObservationRegisters(),
		LoggerPVActivePowerFast, LoggerPVPower, LoggerRatedPVPower,
		LoggerStableChargePower, LoggerStableDischargePower,
		LoggerSteadyChargeableCapacity, LoggerSteadyDischargeableCapacity,
		LoggerEnergyChargedThisMonth, LoggerEnergyDischargedThisMonth,
		LoggerEnergyChargedThisYear, LoggerEnergyDischargedThisYear,
		LoggerTotalEnergyCharged, LoggerTotalEnergyDischarged,
		LoggerExportedToday, LoggerTotalExported, LoggerLoadEnergyToday,
		LoggerPowerFactor, LoggerReactivePower,
		LoggerArrayInOperation, LoggerArrayShutDown, LoggerPCSInOperation, LoggerPCSShutDown,
		LoggerPCSWorkingModeStatus, LoggerTimeZone,
	)
}

// Power meter registers, table 2-5, read at the meter's own RS485 address (11
// on the Pedernoso site). On the meter "a positive value indicates the power
// fed to the grid, and a negative value indicates the power supplied from the
// grid" (SL section 2.4).
var (
	// MeterVoltageA is row 1, "Phase A voltage" at 32260.
	MeterVoltageA = Register{Name: "Phase A voltage", Addr: 32260, Kind: U32, Gain: 100, Unit: "V", Access: RO}
	// MeterVoltageB is row 2 at 32262.
	MeterVoltageB = Register{Name: "Phase B voltage", Addr: 32262, Kind: U32, Gain: 100, Unit: "V", Access: RO}
	// MeterVoltageC is row 3 at 32264.
	MeterVoltageC = Register{Name: "Phase C voltage", Addr: 32264, Kind: U32, Gain: 100, Unit: "V", Access: RO}
	// MeterVoltageAB is row 4, "A-B line voltage" at 32266.
	MeterVoltageAB = Register{Name: "A-B line voltage", Addr: 32266, Kind: U32, Gain: 100, Unit: "V", Access: RO}
	// MeterVoltageBC is row 5 at 32268.
	MeterVoltageBC = Register{Name: "B-C line voltage", Addr: 32268, Kind: U32, Gain: 100, Unit: "V", Access: RO}
	// MeterVoltageCA is row 6 at 32270.
	MeterVoltageCA = Register{Name: "C-A line voltage", Addr: 32270, Kind: U32, Gain: 100, Unit: "V", Access: RO}
	// MeterCurrentA is row 7, "Phase A current" at 32272.
	MeterCurrentA = Register{Name: "Phase A current", Addr: 32272, Kind: I32, Gain: 10, Unit: "A", Access: RO}
	// MeterCurrentB is row 8 at 32274.
	MeterCurrentB = Register{Name: "Phase B current", Addr: 32274, Kind: I32, Gain: 10, Unit: "A", Access: RO}
	// MeterCurrentC is row 9 at 32276.
	MeterCurrentC = Register{Name: "Phase C current", Addr: 32276, Kind: I32, Gain: 10, Unit: "A", Access: RO}

	// MeterActivePower is row 10, "Active power" at 32278: positive = export.
	MeterActivePower = Register{Name: "Active power", Addr: 32278, Kind: I32, Gain: 1000, Unit: "kW", Access: RO}
	// MeterReactivePower is row 11 at 32280.
	MeterReactivePower = Register{Name: "Reactive power", Addr: 32280, Kind: I32, Gain: 1000, Unit: "kVar", Access: RO}
	// MeterPowerFactor is row 13 at 32284.
	MeterPowerFactor = Register{Name: "Power factor", Addr: 32284, Kind: I16, Gain: 1000, Unit: "", Access: RO}
	// MeterApparentPower is row 15 at 32287.
	MeterApparentPower = Register{Name: "Apparent power", Addr: 32287, Kind: I32, Gain: 1000, Unit: "kVA", Access: RO}

	// MeterActivePowerA is row 37, "Phase A active power" at 32335.
	MeterActivePowerA = Register{Name: "Phase A active power", Addr: 32335, Kind: I32, Gain: 1000, Unit: "kW", Access: RO}
	// MeterActivePowerB is row 38 at 32337.
	MeterActivePowerB = Register{Name: "Phase B active power", Addr: 32337, Kind: I32, Gain: 1000, Unit: "kW", Access: RO}
	// MeterActivePowerC is row 39 at 32339.
	MeterActivePowerC = Register{Name: "Phase C active power", Addr: 32339, Kind: I32, Gain: 1000, Unit: "kW", Access: RO}

	// MeterTotalActiveEnergy is row 40, "Total active electricity" at 32341.
	MeterTotalActiveEnergy = Register{Name: "Total active energy", Addr: 32341, Kind: I64, Gain: 100, Unit: "kWh", Access: RO}
	// MeterTotalReactiveEnergy is row 41 at 32345.
	MeterTotalReactiveEnergy = Register{Name: "Total reactive energy", Addr: 32345, Kind: I64, Gain: 100, Unit: "kvarh", Access: RO}
	// MeterNegativeActiveEnergy is row 42, "Negative active electricity" at
	// 32349. Which of negative and positive is import depends on the meter's
	// direction setting; check against MeterActivePower before relying on it.
	MeterNegativeActiveEnergy = Register{Name: "Negative active energy", Addr: 32349, Kind: I64, Gain: 100, Unit: "kWh", Access: RO}
	// MeterNegativeReactiveEnergy is row 43 at 32353.
	MeterNegativeReactiveEnergy = Register{Name: "Negative reactive energy", Addr: 32353, Kind: I64, Gain: 100, Unit: "kvarh", Access: RO}
	// MeterPositiveActiveEnergy is row 44, "Positive active electricity" at 32357.
	MeterPositiveActiveEnergy = Register{Name: "Positive active energy", Addr: 32357, Kind: I64, Gain: 100, Unit: "kWh", Access: RO}
	// MeterPositiveReactiveEnergy is row 45 at 32361.
	MeterPositiveReactiveEnergy = Register{Name: "Positive reactive energy", Addr: 32361, Kind: I64, Gain: 100, Unit: "kvarh", Access: RO}
)

// MeterRegisters is every meter register this package knows. Rows marked
// "(Reserved)", the price-segment counters, the custom slots and the
// configuration block at 33000 are left out.
var MeterRegisters = []Register{
	MeterVoltageA, MeterVoltageB, MeterVoltageC,
	MeterVoltageAB, MeterVoltageBC, MeterVoltageCA,
	MeterCurrentA, MeterCurrentB, MeterCurrentC,
	MeterActivePower, MeterReactivePower, MeterPowerFactor, MeterApparentPower,
	MeterActivePowerA, MeterActivePowerB, MeterActivePowerC,
	MeterTotalActiveEnergy, MeterTotalReactiveEnergy,
	MeterNegativeActiveEnergy, MeterNegativeReactiveEnergy,
	MeterPositiveActiveEnergy, MeterPositiveReactiveEnergy,
}
