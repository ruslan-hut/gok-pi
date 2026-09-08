package huawei

import "sort"

// MaxReadCount is the number of registers a single 0x03 request may ask for
// (section 4.3.3.1).
const MaxReadCount = 125

// DefaultGap is the number of undocumented registers worth pulling along to
// avoid a second round trip. The point table is sparse — the alarm words sit in
// three tight clusters, the telemetry in a handful more — so a small gap
// collapses a hundred-odd signals into under a dozen requests, while a large
// one would drag in wide undocumented spans that some devices answer with
// exception 0x02.
const DefaultGap = 8

// Block is a contiguous span of holding registers fetched in one 0x03 request.
type Block struct {
	Addr  uint16
	Count uint16
}

// End is the address one past the last register of the block.
func (b Block) End() uint16 { return b.Addr + b.Count }

// Contains reports whether every word of r falls inside b.
func (b Block) Contains(r Register) bool {
	return r.Addr >= b.Addr && r.Addr+r.Words() <= b.End()
}

// Extract returns the words of r out of a block read that started at b.Addr.
// It reports false if r does not lie inside b, or if words is not this block.
func (b Block) Extract(words []uint16, r Register) ([]uint16, bool) {
	if len(words) != int(b.Count) || !b.Contains(r) {
		return nil, false
	}

	off := int(r.Addr - b.Addr)

	return words[off : off+int(r.Words())], true
}

// Blocks packs regs into the fewest read requests, merging spans separated by
// no more than gap undocumented registers and splitting at MaxReadCount. Pass
// DefaultGap unless there is a reason not to. The result is ordered by address,
// and duplicate or overlapping registers are absorbed rather than repeated.
func Blocks(regs []Register, gap uint16) []Block {
	if len(regs) == 0 {
		return nil
	}

	sorted := make([]Register, len(regs))
	copy(sorted, regs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Addr < sorted[j].Addr })

	var out []Block
	cur := Block{Addr: sorted[0].Addr, Count: sorted[0].Words()}

	for _, r := range sorted[1:] {
		end := r.Addr + r.Words()
		if end <= cur.End() {
			continue // wholly inside the block already being built
		}

		// Extending must stay within one request and must not skip more than gap.
		if r.Addr <= cur.End()+gap && end-cur.Addr <= MaxReadCount {
			cur.Count = end - cur.Addr

			continue
		}

		out = append(out, cur)
		cur = Block{Addr: r.Addr, Count: r.Words()}
	}

	return append(out, cur)
}

// TelemetryRegisters are the signals worth sampling on every poll: state of
// charge and power for control, the capability and capacity limits that bound
// it, and the counters and identifiers that make a reading interpretable later.
var TelemetryRegisters = []Register{
	SOC, ControlSOC, SOH, SOE, DOD,
	ChargeDischargePower, ChargeableCapacity, DischargeableCapacity,
	RatedCapacity, RatedPower, MaxActivePower, MaxReverseRectificationPower,
	ActualChargePowerCapability, ActualDischargePowerCapability,
	WorkStatus, ChargingStatus, RackVoltage, RackCurrent,
	ActivePower, ReactivePower, PowerFactor, GridFrequency,
	EnergyDischargedToday, EnergyChargedToday,
	TotalEnergyDischarged, TotalEnergyCharged,
	HighestPackTemperature, LowestPackTemperature,
	LocalTime,
}

// SetpointRegisters are the writable registers worth sampling even though this
// package never writes them. They are readable, so polling them shows whether
// something else on the network is dispatching the ESS: a setpoint that moves
// on its own is another master at work.
var SetpointRegisters = []Register{
	PowerOnOff, WorkingMode,
	ActivePowerSetpoint, ActivePowerPercent, ActivePowerBaseline,
	ReactivePowerCompensation, PowerFactorSetpoint,
	ChargeCutOffSOC, DischargeCutOffSOC,
	GridCode, SystemTime,
}

// ObservationRegisters is everything a passive observer should sample: the
// telemetry, the setpoints that reveal a competing master, and every alarm word.
func ObservationRegisters() []Register {
	out := make([]Register, 0, len(TelemetryRegisters)+len(SetpointRegisters)+len(AlarmWords))
	out = append(out, TelemetryRegisters...)
	out = append(out, SetpointRegisters...)
	out = append(out, AlarmWords[:]...)

	return out
}

// AllRegisters is every register this package knows, for a full dump.
func AllRegisters() []Register {
	out := ObservationRegisters()
	out = append(out, GridCodeMasks[:]...)
	out = append(out,
		City, DaylightSavingEnable,
		AuxEnergyConsumption, AuxActivePower, AuxReactivePower, AuxPowerFactor,
		CabinTemperature1, CabinHumidity1, CabinTemperature2, CabinHumidity2,
		COConcentration1, ExhaustFanSpeed1, ExhaustFanSpeed2,
		LTMSWorkingStatus, OutdoorTemperature,
		BatterySupplyWaterTemperature, BatteryReturnWaterTemperature,
		PowerSupplyWaterTemperature, PowerReturnWaterTemperature,
		PackOfHighestTemperature, PackOfLowestTemperature,
		LowestPackVoltage, PackOfLowestVoltage, HighestPackVoltage, PackOfHighestVoltage,
		GridVoltageAB, GridVoltageBC, GridVoltageCA,
		GridVoltageA, GridVoltageB, GridVoltageC,
		GridCurrentA, GridCurrentB, GridCurrentC,
		DCVoltage, DCCurrent,
		EnergyDischargedThisMonth, EnergyChargedThisMonth,
		EnergyDischargedThisYear, EnergyChargedThisYear,
	)

	for pack := 1; pack <= PackCount; pack++ {
		out = append(out, PackVoltage(pack), PackSOC(pack), PackSOH(pack))
	}

	return out
}
