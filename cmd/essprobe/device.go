package main

import (
	"fmt"
	"sort"
	"strings"

	"gok-pi/battery/driver/huawei"
)

// device is one kind of endpoint behind a Huawei Modbus-TCP address: an ESS
// cabinet, the SmartLogger itself at unit 0, or the power meter on its RS485
// address. Each has its own point table, so everything essprobe reads and how
// it explains the values depends on which one -device names.
type device struct {
	name string

	// observation is what dump reads by default and watch samples; all is
	// what dump -all reads. Both return a fresh slice, since dump sorts it.
	observation func() []huawei.Register
	all         func() []huawei.Register

	// telemetry and setpoints split the observation set in watch's output.
	// A setpoint that moves was written by another master.
	telemetry []huawei.Register
	setpoints []huawei.Register

	// alarmWords and alarmsIn decode the device's alarm bitfields; a device
	// without alarm registers leaves both empty.
	alarmWords []huawei.Register
	alarmsIn   func(addr, word uint16) []huawei.Alarm

	// soc and polarity drive watch's sign check: each power register's sign is
	// correlated against the direction soc moves.
	soc      huawei.Register
	polarity []polarity

	// nameplate is identify's mapping check, status the enumerations it shows
	// next to it, and footer a closing hint.
	nameplate []check
	status    []huawei.Register
	footer    string

	// annotate explains a raw value where the document defines a meaning.
	annotate func(reg huawei.Register, s *sample) string
}

// check is one line of identify's nameplate check.
type check struct {
	label   string
	reg     huawei.Register
	verdict func(float64) string
}

// polarity is a power register whose sign watch checks against SOC. confirmed
// is the convention already settled on site: +1 when positive means charging,
// -1 when positive means discharging, 0 when it is not yet known.
type polarity struct {
	reg       huawei.Register
	confirmed int
}

var devices = map[string]*device{
	"ess":    essDevice,
	"logger": loggerDevice,
	"meter":  meterDevice,
}

// deviceNames lists the -device values, for usage and error messages.
func deviceNames() string {
	names := make([]string, 0, len(devices))
	for n := range devices {
		names = append(names, n)
	}
	sort.Strings(names)

	return strings.Join(names, ", ")
}

var essDevice = &device{
	name:        "ess",
	observation: huawei.ObservationRegisters,
	all:         huawei.AllRegisters,
	telemetry:   huawei.TelemetryRegisters,
	setpoints:   huawei.SetpointRegisters,
	alarmWords:  huawei.AlarmWords[:],
	alarmsIn:    huawei.AlarmsInWord,
	soc:         huawei.ControlSOC,
	polarity: []polarity{
		// Both settled on site on 2026-09-15: 30417 is battery-side, 32986
		// is AC-side, and they run opposite ways.
		{reg: huawei.ChargeDischargePower, confirmed: +1},
		{reg: huawei.ActivePower, confirmed: -1},
	},
	// RatedCapacity is a U32 with a gain of 1000, so a LUNA2000-215 that
	// decodes to 215.0 confirms the address space, the word order and the gain
	// in a single read.
	nameplate: []check{
		{"rated capacity", huawei.RatedCapacity, func(v float64) string {
			if v <= 0 || v > 10000 {
				return "IMPLAUSIBLE - the point table does not match this device"
			}

			return "compare against the installed model's nameplate"
		}},
		{"rated power", huawei.RatedPower, plausibleRange(0, 10000)},
		{"max active power (Pmax)", huawei.MaxActivePower, plausibleRange(0, 10000)},
		{"max reverse power (RPmax)", huawei.MaxReverseRectificationPower, plausibleRange(-10000, 10000)},
		{"SOC", huawei.SOC, plausibleRange(0, 100)},
	},
	status: []huawei.Register{huawei.WorkStatus},
	footer: "Active power setpoint registers are readable and are polled by 'watch'.\n" +
		"A setpoint that moves without you writing it means another master is dispatching this ESS.",
	annotate: annotateESS,
}

var loggerDevice = &device{
	name:        "logger",
	observation: huawei.LoggerObservationRegisters,
	all:         huawei.LoggerAllRegisters,
	telemetry:   huawei.LoggerTelemetryRegisters,
	setpoints:   huawei.LoggerSetpointRegisters,
	alarmWords:  huawei.LoggerAlarmWords[:],
	alarmsIn:    huawei.LoggerAlarmsInWord,
	soc:         huawei.LoggerSOC,
	// None of the three is documented with a sign; watching them against SOC
	// is how they get settled.
	polarity: []polarity{
		{reg: huawei.LoggerESSChargeDischargePower},
		{reg: huawei.LoggerESSActivePower},
		{reg: huawei.LoggerESSActivePowerFast},
	},
	// Rated ESS capacity is the sum of every cabinet's RatedCapacity, so it
	// plays the same role at unit 0: one read that confirms address space,
	// word order and gain.
	nameplate: []check{
		{"rated ESS capacity", huawei.LoggerRatedESSCapacity, func(v float64) string {
			if v <= 0 || v > 100000 {
				return "IMPLAUSIBLE - the point table does not match this device"
			}

			return "must equal the sum of the cabinets' rated capacity"
		}},
		{"rated ESS power", huawei.LoggerRatedESSPower, plausibleRange(0, 100000)},
		{"number of ESSs", huawei.LoggerNumberOfESSs, plausibleRange(1, 100)},
		{"number of PCSs", huawei.LoggerNumberOfPCSs, plausibleRange(1, 100)},
		{"running PCSs", huawei.LoggerRunningPCSs, plausibleRange(0, 100)},
		{"max active power adjustment", huawei.LoggerMaxActivePowerAdjustment, plausibleRange(0, 100000)},
		{"min active power adjustment", huawei.LoggerMinActivePowerAdjustment, plausibleRange(-100000, 0)},
		{"SOC", huawei.LoggerSOC, plausibleRange(0, 100)},
	},
	status: []huawei.Register{
		huawei.LoggerActivePowerControlMode, huawei.LoggerActivePowerControlMethod,
		huawei.LoggerSchedulingTarget, huawei.LoggerESSActivePowerSetpoint,
		huawei.LoggerStatus, // bit 0 reports a 40430 override
		huawei.LoggerArrayChargeEndSOC, huawei.LoggerArrayDischargeEndSOC,
	},
	footer: "40381/40383 are the plant-level ESS dispatch registers; 'watch' reports any change to them.\n" +
		"Scheduling target (40738) is what the logger is dispatching right now.",
	annotate: annotateLogger,
}

var meterDevice = &device{
	name:        "meter",
	observation: func() []huawei.Register { return append([]huawei.Register(nil), huawei.MeterRegisters...) },
	all:         func() []huawei.Register { return append([]huawei.Register(nil), huawei.MeterRegisters...) },
	telemetry:   huawei.MeterRegisters,
	nameplate: []check{
		{"phase A voltage", huawei.MeterVoltageA, plausibleRange(100, 300)},
		{"phase B voltage", huawei.MeterVoltageB, plausibleRange(100, 300)},
		{"phase C voltage", huawei.MeterVoltageC, plausibleRange(100, 300)},
		{"A-B line voltage", huawei.MeterVoltageAB, plausibleRange(170, 520)},
	},
	status: []huawei.Register{huawei.MeterActivePower},
	footer: "On this meter positive active power is export to the grid, negative is import (SL section 2.4).",
	annotate: func(reg huawei.Register, s *sample) string {
		switch reg.Addr {
		case huawei.MeterActivePower.Addr, huawei.MeterActivePowerA.Addr,
			huawei.MeterActivePowerB.Addr, huawei.MeterActivePowerC.Addr:
			return gridDirection(s, reg)
		}

		return ""
	},
}

// annotateESS adds the meaning behind a cabinet register's raw value.
func annotateESS(reg huawei.Register, s *sample) string {
	raw, ok := s.raw(reg)
	if !ok {
		return ""
	}
	v := uint16(raw)

	switch reg.Addr {
	case huawei.WorkStatus.Addr:
		return huawei.WorkStatusName(v)
	case huawei.ChargingStatus.Addr:
		return [...]string{"idle", "recharge request", "recharging", "charging ends"}[min(int(v), 3)]
	case huawei.LTMSWorkingStatus.Addr:
		return [...]string{"off", "self-circulating", "refrigeration", "heating"}[min(int(v), 3)]
	case huawei.WorkingMode.Addr:
		if v == huawei.ModeVSG {
			return "VSG (grid-forming)"
		}

		return "PQ (grid-following)"
	case huawei.PowerOnOff.Addr:
		if v == huawei.PowerStateRun {
			return "run"
		}

		return "off"
	case huawei.ChargeDischargePower.Addr:
		return powerDirection(s, huawei.ChargeDischargePower, +1)
	case huawei.ActivePower.Addr:
		return powerDirection(s, huawei.ActivePower, -1)
	}

	if reg.Kind == huawei.Bits16 && v != 0 {
		return alarmNote(huawei.AlarmsInWord(reg.Addr, v), v)
	}

	return ""
}

// annotateLogger adds the meaning behind a SmartLogger register's raw value.
func annotateLogger(reg huawei.Register, s *sample) string {
	raw, ok := s.raw(reg)
	if !ok {
		return ""
	}
	v := uint16(raw)

	switch reg.Addr {
	case huawei.LoggerActivePowerControlMode.Addr:
		return huawei.ControlModeName(v)
	case huawei.LoggerActivePowerControlMethod.Addr:
		return "if it follows 40737: " + huawei.ControlModeName(v)
	case huawei.LoggerHighestPriorityActivePower.Addr:
		if raw == huawei.ReleaseHighestPriority {
			return "released"
		}

		return "set: overrides every other active-power port"
	case huawei.LoggerStatus.Addr:
		if huawei.Bit(v, 0) {
			return "40430 override active"
		}

		return "no override"
	case huawei.LoggerPPCCommStatus.Addr:
		if huawei.Bit(v, 0) {
			return "abnormal"
		}

		return "normal"
	case huawei.LoggerPCSWorkingModeStatus.Addr:
		switch v {
		case 0:
			return "PQ"
		case 1:
			return "VSG"
		case 2, 3:
			return "mixed PQ/VSG"
		}

		return "unknown"
	case huawei.LoggerESSChargeDischargePower.Addr, huawei.LoggerESSActivePower.Addr,
		huawei.LoggerESSActivePowerFast.Addr:
		return powerDirection(s, reg, 0)
	case huawei.LoggerESSActivePowerSetpoint.Addr, huawei.LoggerESSActivePowerPercent.Addr:
		return powerDirection(s, reg, -1)
	}

	if reg.Kind == huawei.Bits16 && v != 0 {
		return alarmNote(huawei.LoggerAlarmsInWord(reg.Addr, v), v)
	}

	return ""
}

// alarmNote lists the alarms a word raises, or says the set bits are
// undocumented.
func alarmNote(active []huawei.Alarm, word uint16) string {
	if len(active) == 0 {
		return fmt.Sprintf("bits 0x%04X set, none documented", word)
	}

	labels := make([]string, len(active))
	for i, a := range active {
		labels[i] = a.Label()
	}

	return strings.Join(labels, "; ")
}

// powerDirection spells out the sign of a power reading. convention is +1 when
// positive is known to mean charging (30417, confirmed on site), -1 when it is
// known to mean discharging (AC-side registers and the setpoints), and 0 when
// the sign has not been settled yet.
func powerDirection(s *sample, reg huawei.Register, convention int) string {
	v, ok := s.value(reg)
	if !ok || v == 0 {
		return "idle"
	}

	switch {
	case convention == 0 && v > 0:
		return "positive (convention unconfirmed)"
	case convention == 0:
		return "negative (convention unconfirmed)"
	case (v > 0) == (convention > 0):
		return sign(v) + " (charging)"
	default:
		return sign(v) + " (discharging)"
	}
}

// gridDirection spells out a meter power reading: positive is export.
func gridDirection(s *sample, reg huawei.Register) string {
	v, ok := s.value(reg)
	switch {
	case !ok || v == 0:
		return ""
	case v > 0:
		return "export to grid"
	default:
		return "import from grid"
	}
}

func sign(v float64) string {
	if v > 0 {
		return "positive"
	}

	return "negative"
}
