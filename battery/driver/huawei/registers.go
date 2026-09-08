// Package huawei implements Modbus-TCP access to Huawei LUNA2000B C&I energy
// storage systems.
//
// The register addresses, data types, gains and value ranges in this file are
// transcribed from "LUNA2000B ESS Modbus Port Definitions", issue 01
// (2025-09-10), table 3-1 "C&I cabinet subsystem". Row numbers in the doc
// comments refer to the No. column of that table, so a value can be checked
// against the source without re-deriving it.
//
// Addresses are literal Modbus PDU addresses: the document's own example
// (section 4.3.3.4) reads row 32306 as 0x7E32, so there is no 40001-style
// offset to apply. The ESS answers on TCP port 502 and accepts only function
// codes 0x03, 0x06 and 0x10 (section 4.3.1); every register below lives in the
// holding-register space.
package huawei

import "fmt"

// Kind is the wire representation of a register value.
type Kind uint8

const (
	U16 Kind = iota
	U32
	I16
	I32
	// Bits16 is the document's Bitfield16: sixteen independent flags, carried
	// as a U16 but never scaled by Gain.
	Bits16
)

// Words is the number of 16-bit registers a value of this Kind occupies. It is
// the Quantity column of table 3-1.
func (k Kind) Words() uint16 {
	switch k {
	case U32, I32:
		return 2
	default:
		return 1
	}
}

// Signed reports whether the raw value is two's complement.
func (k Kind) Signed() bool { return k == I16 || k == I32 }

// String implements fmt.Stringer.
func (k Kind) String() string {
	switch k {
	case U16:
		return "U16"
	case U32:
		return "U32"
	case I16:
		return "I16"
	case I32:
		return "I32"
	case Bits16:
		return "Bitfield16"
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// Access is the Read/Write column of table 3-1.
type Access uint8

const (
	RO Access = iota
	RW
	WO
)

// Readable reports whether the register may be fetched with function code 0x03.
func (a Access) Readable() bool { return a != WO }

// Writable reports whether the register may be set with function code 0x06 or 0x10.
func (a Access) Writable() bool { return a != RO }

// String implements fmt.Stringer.
func (a Access) String() string {
	switch a {
	case RO:
		return "RO"
	case RW:
		return "RW"
	case WO:
		return "WO"
	}
	return fmt.Sprintf("Access(%d)", uint8(a))
}

// Register describes one signal of the LUNA2000B C&I cabinet point table.
//
// Gain relates the raw integer on the wire to the engineering value in Unit:
// the engineering value is raw/Gain, so a Gain of 1000 on a kW register means
// the wire carries watts. Gain is never zero.
//
// Min and Max carry the documented value range, in engineering units, but only
// for writable registers whose Scope column gives plain numeric bounds; Bounded
// says whether they are meaningful. Registers whose range is dynamic
// (ActivePowerSetpoint, bounded at runtime by MaxReverseRectificationPower and
// MaxActivePower) or non-contiguous (PowerFactorSetpoint, two disjoint
// intervals) are left unbounded and must be checked by the caller.
type Register struct {
	Name    string
	Addr    uint16
	Kind    Kind
	Gain    int
	Unit    string
	Access  Access
	Min     float64
	Max     float64
	Bounded bool
}

// Words is the number of 16-bit registers this signal occupies.
func (r Register) Words() uint16 { return r.Kind.Words() }

// String implements fmt.Stringer.
func (r Register) String() string { return fmt.Sprintf("%s@%d", r.Name, r.Addr) }

// Registers of table 3-1. Alarm and grid-code-mask words are repetitive and are
// grouped into AlarmWords and GridCodeMasks below; the per-pack signals are
// reached through PackVoltage, PackSOC and PackSOH.
var (
	// LocalTime is point-table row 1, "Local time" at 30165.
	// Scope: Epoch second
	LocalTime = Register{Name: "Local time", Addr: 30165, Kind: U32, Gain: 1, Unit: "", Access: RO}

	// RatedCapacity is point-table row 2, "Rated capacity" at 30236.
	RatedCapacity = Register{Name: "Rated capacity", Addr: 30236, Kind: U32, Gain: 1000, Unit: "kWh", Access: RO}

	// RatedPower is point-table row 3, "Rated power" at 30238.
	RatedPower = Register{Name: "Rated power", Addr: 30238, Kind: U32, Gain: 1000, Unit: "kW", Access: RO}

	// SOC is point-table row 4, "SOC" at 30400.
	// Scope: [0, 100]
	SOC = Register{Name: "SOC", Addr: 30400, Kind: U16, Gain: 1, Unit: "%", Access: RO}

	// ChargeDischargePower is point-table row 5, "Charge/Discharge power" at 30417.
	ChargeDischargePower = Register{Name: "Charge/Discharge power", Addr: 30417, Kind: I32, Gain: 1000, Unit: "kW", Access: RO}

	// ChargeableCapacity is point-table row 6, "Chargeable capacity" at 30419.
	ChargeableCapacity = Register{Name: "Chargeable capacity", Addr: 30419, Kind: U32, Gain: 1000, Unit: "kWh", Access: RO}

	// DischargeableCapacity is point-table row 7, "Dischargeable capacity" at 30421.
	DischargeableCapacity = Register{Name: "Dischargeable capacity", Addr: 30421, Kind: U32, Gain: 1000, Unit: "kWh", Access: RO}

	// AuxEnergyConsumption is point-table row 8, "Total auxiliary power consumption" at 30453.
	AuxEnergyConsumption = Register{Name: "Total auxiliary power consumption", Addr: 30453, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// AuxActivePower is point-table row 21, "Active power of auxiliary power supply" at 30497.
	AuxActivePower = Register{Name: "Active power of auxiliary power supply", Addr: 30497, Kind: I32, Gain: 1000, Unit: "kW", Access: RO}

	// AuxReactivePower is point-table row 22, "Reactive power of auxiliary power supply" at 30499.
	AuxReactivePower = Register{Name: "Reactive power of auxiliary power supply", Addr: 30499, Kind: I32, Gain: 1000, Unit: "kVar", Access: RO}

	// AuxPowerFactor is point-table row 23, "Power factor of auxiliary power supply" at 30501.
	AuxPowerFactor = Register{Name: "Power factor of auxiliary power supply", Addr: 30501, Kind: I16, Gain: 1000, Unit: "", Access: RO}

	// CabinTemperature1 is point-table row 32, "Battery cabin temperature 1" at 30703.
	CabinTemperature1 = Register{Name: "Battery cabin temperature 1", Addr: 30703, Kind: I16, Gain: 10, Unit: "°C", Access: RO}

	// CabinHumidity1 is point-table row 33, "Battery cabin humidity 1" at 30704.
	CabinHumidity1 = Register{Name: "Battery cabin humidity 1", Addr: 30704, Kind: I16, Gain: 10, Unit: "%", Access: RO}

	// CabinTemperature2 is point-table row 34, "Battery cabin temperature 2" at 30705.
	CabinTemperature2 = Register{Name: "Battery cabin temperature 2", Addr: 30705, Kind: I16, Gain: 10, Unit: "°C", Access: RO}

	// CabinHumidity2 is point-table row 35, "Battery cabin humidity 2" at 30706.
	CabinHumidity2 = Register{Name: "Battery cabin humidity 2", Addr: 30706, Kind: I16, Gain: 10, Unit: "%", Access: RO}

	// COConcentration1 is point-table row 36, "CO concentration 1" at 30715.
	COConcentration1 = Register{Name: "CO concentration 1", Addr: 30715, Kind: U16, Gain: 1, Unit: "ppm", Access: RO}

	// ExhaustFanSpeed1 is point-table row 37, "Exhaust fan speed 1" at 30735.
	ExhaustFanSpeed1 = Register{Name: "Exhaust fan speed 1", Addr: 30735, Kind: U16, Gain: 1, Unit: "RPM", Access: RO}

	// ExhaustFanSpeed2 is point-table row 38, "Exhaust fan speed 2" at 30736.
	// Scope: N/A
	ExhaustFanSpeed2 = Register{Name: "Exhaust fan speed 2", Addr: 30736, Kind: U16, Gain: 1, Unit: "RPM", Access: RO}

	// LTMSWorkingStatus is point-table row 39, "LTMS Working status" at 31400.
	// Scope: 0: Off 1: Self-circulating 2: Refrigeration 3: Heating
	LTMSWorkingStatus = Register{Name: "LTMS Working status", Addr: 31400, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// OutdoorTemperature is point-table row 40, "Outdoor temperature" at 31411.
	// Scope: [-50.0, 150.0]
	OutdoorTemperature = Register{Name: "Outdoor temperature", Addr: 31411, Kind: I16, Gain: 10, Unit: "°C", Access: RO}

	// BatterySupplyWaterTemperature is point-table row 41, "Battery-side supply water temperature" at 31473.
	// Scope: [-50.0, 150.0]
	BatterySupplyWaterTemperature = Register{Name: "Battery-side supply water temperature", Addr: 31473, Kind: I16, Gain: 10, Unit: "°C", Access: RO}

	// BatteryReturnWaterTemperature is point-table row 42, "Battery-side return water temperature" at 31474.
	// Scope: [-50.0, 150.0]
	BatteryReturnWaterTemperature = Register{Name: "Battery-side return water temperature", Addr: 31474, Kind: I16, Gain: 10, Unit: "°C", Access: RO}

	// PowerSupplyWaterTemperature is point-table row 43, "Power-side supply water temperature" at 31475.
	// Scope: [-50.0, 150.0]
	PowerSupplyWaterTemperature = Register{Name: "Power-side supply water temperature", Addr: 31475, Kind: I16, Gain: 10, Unit: "°C", Access: RO}

	// PowerReturnWaterTemperature is point-table row 44, "Power-side return water temperature" at 31476.
	// Scope: [-50.0, 150.0]
	PowerReturnWaterTemperature = Register{Name: "Power-side return water temperature", Addr: 31476, Kind: I16, Gain: 10, Unit: "°C", Access: RO}

	// WorkStatus is point-table row 45, "Work status" at 32034.
	WorkStatus = Register{Name: "Work status", Addr: 32034, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// RackVoltage is point-table row 46, "Rack voltage" at 32036.
	// Scope: N/A
	RackVoltage = Register{Name: "Rack voltage", Addr: 32036, Kind: I16, Gain: 10, Unit: "V", Access: RO}

	// RackCurrent is point-table row 47, "Rack current" at 32037.
	// Scope: N/A
	RackCurrent = Register{Name: "Rack current", Addr: 32037, Kind: I16, Gain: 10, Unit: "A", Access: RO}

	// SOH is point-table row 48, "SOH" at 32041.
	// Scope: [0,100]
	SOH = Register{Name: "SOH", Addr: 32041, Kind: U16, Gain: 1, Unit: "%", Access: RO}

	// SOE is point-table row 49, "SOE" at 32042.
	// Scope: [0,100]
	SOE = Register{Name: "SOE", Addr: 32042, Kind: U16, Gain: 1, Unit: "%", Access: RO}

	// DOD is point-table row 50, "DOD" at 32043.
	// Scope: [0,100]
	DOD = Register{Name: "DOD", Addr: 32043, Kind: U16, Gain: 1, Unit: "%", Access: RO}

	// ControlSOC is point-table row 51, "Control SOC" at 32044.
	// Scope: [0.0,100.0]
	ControlSOC = Register{Name: "Control SOC", Addr: 32044, Kind: U16, Gain: 10, Unit: "%", Access: RO}

	// ChargingStatus is point-table row 52, "Charging status" at 32046.
	// Scope: 0:Idle 1:Recharge request 2:Recharging 3:Charging ends
	ChargingStatus = Register{Name: "Charging status", Addr: 32046, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// HighestPackTemperature is point-table row 53, "Highest pack temperature" at 32066.
	// Scope: N/A
	HighestPackTemperature = Register{Name: "Highest pack temperature", Addr: 32066, Kind: I16, Gain: 100, Unit: "°C", Access: RO}

	// PackOfHighestTemperature is point-table row 54, "Pack of highest temperature" at 32067.
	// Scope: N/A
	PackOfHighestTemperature = Register{Name: "Pack of highest temperature", Addr: 32067, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// LowestPackTemperature is point-table row 55, "Lowest pack temperature" at 32068.
	// Scope: N/A
	LowestPackTemperature = Register{Name: "Lowest pack temperature", Addr: 32068, Kind: I16, Gain: 100, Unit: "°C", Access: RO}

	// PackOfLowestTemperature is point-table row 56, "Pack of lowest temperature" at 32069.
	// Scope: N/A
	PackOfLowestTemperature = Register{Name: "Pack of lowest temperature", Addr: 32069, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// LowestPackVoltage is point-table row 57, "Lowest pack voltage" at 32070.
	// Scope: N/A
	LowestPackVoltage = Register{Name: "Lowest pack voltage", Addr: 32070, Kind: U16, Gain: 10, Unit: "V", Access: RO}

	// PackOfLowestVoltage is point-table row 58, "Pack of lowest voltage" at 32071.
	// Scope: N/A
	PackOfLowestVoltage = Register{Name: "Pack of lowest voltage", Addr: 32071, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// HighestPackVoltage is point-table row 59, "Highest pack voltage" at 32072.
	// Scope: N/A
	HighestPackVoltage = Register{Name: "Highest pack voltage", Addr: 32072, Kind: U16, Gain: 10, Unit: "V", Access: RO}

	// PackOfHighestVoltage is point-table row 60, "Pack of highest voltage" at 32073.
	// Scope: N/A
	PackOfHighestVoltage = Register{Name: "Pack of highest voltage", Addr: 32073, Kind: U16, Gain: 1, Unit: "", Access: RO}

	// MaxActivePower is point-table row 93, "Maximum active power (Pmax)" at 32853.
	MaxActivePower = Register{Name: "Maximum active power (Pmax)", Addr: 32853, Kind: U32, Gain: 1000, Unit: "kW", Access: RO}

	// GridVoltageAB is point-table row 126, "AB line voltage of power grid" at 32970.
	GridVoltageAB = Register{Name: "AB line voltage of power grid", Addr: 32970, Kind: U16, Gain: 10, Unit: "V", Access: RO}

	// GridVoltageBC is point-table row 127, "BC line voltage of power grid" at 32971.
	GridVoltageBC = Register{Name: "BC line voltage of power grid", Addr: 32971, Kind: U16, Gain: 10, Unit: "V", Access: RO}

	// GridVoltageCA is point-table row 128, "CA line voltage of power grid" at 32972.
	GridVoltageCA = Register{Name: "CA line voltage of power grid", Addr: 32972, Kind: U16, Gain: 10, Unit: "V", Access: RO}

	// GridVoltageA is point-table row 129, "Phase A voltage of power grid" at 32973.
	GridVoltageA = Register{Name: "Phase A voltage of power grid", Addr: 32973, Kind: U16, Gain: 10, Unit: "V", Access: RO}

	// GridVoltageB is point-table row 130, "Phase B voltage of power grid" at 32974.
	GridVoltageB = Register{Name: "Phase B voltage of power grid", Addr: 32974, Kind: U16, Gain: 10, Unit: "V", Access: RO}

	// GridVoltageC is point-table row 131, "Phase C voltage of power grid" at 32975.
	GridVoltageC = Register{Name: "Phase C voltage of power grid", Addr: 32975, Kind: U16, Gain: 10, Unit: "V", Access: RO}

	// GridCurrentA is point-table row 132, "Phase A current of power grid" at 32976.
	GridCurrentA = Register{Name: "Phase A current of power grid", Addr: 32976, Kind: I32, Gain: 1000, Unit: "A", Access: RO}

	// GridCurrentB is point-table row 133, "Phase B current of power grid" at 32978.
	GridCurrentB = Register{Name: "Phase B current of power grid", Addr: 32978, Kind: I32, Gain: 1000, Unit: "A", Access: RO}

	// GridCurrentC is point-table row 134, "Phase C current of power grid" at 32980.
	GridCurrentC = Register{Name: "Phase C current of power grid", Addr: 32980, Kind: I32, Gain: 1000, Unit: "A", Access: RO}

	// ActivePower is point-table row 135, "Active power" at 32986.
	ActivePower = Register{Name: "Active power", Addr: 32986, Kind: I32, Gain: 1000, Unit: "kW", Access: RO}

	// ReactivePower is point-table row 136, "Reactive power" at 33000.
	ReactivePower = Register{Name: "Reactive power", Addr: 33000, Kind: I32, Gain: 1000, Unit: "kVar", Access: RO}

	// PowerFactor is point-table row 137, "Power factor" at 33020.
	PowerFactor = Register{Name: "Power factor", Addr: 33020, Kind: I16, Gain: 1000, Unit: "", Access: RO}

	// GridFrequency is point-table row 138, "Grid Frequency" at 33021.
	GridFrequency = Register{Name: "Grid Frequency", Addr: 33021, Kind: U16, Gain: 100, Unit: "Hz", Access: RO}

	// DCVoltage is point-table row 139, "DC voltage" at 33024.
	DCVoltage = Register{Name: "DC voltage", Addr: 33024, Kind: U16, Gain: 10, Unit: "V", Access: RO}

	// DCCurrent is point-table row 140, "DC current" at 33025.
	DCCurrent = Register{Name: "DC current", Addr: 33025, Kind: I32, Gain: 100, Unit: "A", Access: RO}

	// MaxReverseRectificationPower is point-table row 141, "Maximum active power of reverse rectification(RPmax)" at 33097.
	MaxReverseRectificationPower = Register{Name: "Maximum active power of reverse rectification(RPmax)", Addr: 33097, Kind: I32, Gain: 1000, Unit: "kW", Access: RO}

	// EnergyDischargedToday is point-table row 142, "Energy discharged today" at 33152.
	EnergyDischargedToday = Register{Name: "Energy discharged today", Addr: 33152, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// EnergyChargedToday is point-table row 143, "Energy charged today" at 33154.
	EnergyChargedToday = Register{Name: "Energy charged today", Addr: 33154, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// EnergyDischargedThisMonth is point-table row 144, "Energy discharged this month" at 33156.
	EnergyDischargedThisMonth = Register{Name: "Energy discharged this month", Addr: 33156, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// EnergyChargedThisMonth is point-table row 145, "Energy charged this month" at 33158.
	EnergyChargedThisMonth = Register{Name: "Energy charged this month", Addr: 33158, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// EnergyDischargedThisYear is point-table row 146, "Energy discharged this year" at 33160.
	EnergyDischargedThisYear = Register{Name: "Energy discharged this year", Addr: 33160, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// EnergyChargedThisYear is point-table row 147, "Energy charged this year" at 33162.
	EnergyChargedThisYear = Register{Name: "Energy charged this year", Addr: 33162, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// TotalEnergyDischarged is point-table row 148, "Total energy discharged" at 33164.
	TotalEnergyDischarged = Register{Name: "Total energy discharged", Addr: 33164, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// TotalEnergyCharged is point-table row 149, "Total energy charged" at 33166.
	TotalEnergyCharged = Register{Name: "Total energy charged", Addr: 33166, Kind: U32, Gain: 100, Unit: "kWh", Access: RO}

	// ActualChargePowerCapability is point-table row 150, "Actual charging power capability" at 33215.
	// Scope: PCS Actual charging power capability value
	ActualChargePowerCapability = Register{Name: "Actual charging power capability", Addr: 33215, Kind: U32, Gain: 1000, Unit: "kW", Access: RO}

	// ActualDischargePowerCapability is point-table row 151, "Actual discharge power capability" at 33217.
	// Scope: PCS Actual discharge power capability value
	ActualDischargePowerCapability = Register{Name: "Actual discharge power capability", Addr: 33217, Kind: U32, Gain: 1000, Unit: "kW", Access: RO}

	// SystemTime is point-table row 155, "System Time" at 40000.
	// Scope: Epoch second UTC
	SystemTime = Register{Name: "System Time", Addr: 40000, Kind: U32, Gain: 1, Unit: "", Access: RW}

	// City is point-table row 156, "City" at 40002.
	City = Register{Name: "City", Addr: 40002, Kind: U32, Gain: 1, Unit: "", Access: RW}

	// DaylightSavingEnable is point-table row 157, "Daylight saving time enable" at 40004.
	// Scope: 0:Disable 1:Enable
	DaylightSavingEnable = Register{Name: "Daylight saving time enable", Addr: 40004, Kind: U16, Gain: 1, Unit: "", Access: RW}

	// PowerOnOff is point-table row 158, "Power-on or power-off of Energy storage" at 42000.
	// Scope: 1: Run 2: Off
	PowerOnOff = Register{Name: "Power-on or power-off of Energy storage", Addr: 42000, Kind: U16, Gain: 1, Unit: "", Access: RW}

	// ChargeCutOffSOC is point-table row 159, "Charging cut-off SOC" at 42002.
	// Scope: [90,100]
	ChargeCutOffSOC = Register{Name: "Charging cut-off SOC", Addr: 42002, Kind: U16, Gain: 1, Unit: "%", Access: RW, Min: 90, Max: 100, Bounded: true}

	// DischargeCutOffSOC is point-table row 160, "Discharge cut-off SOC" at 42003.
	// Scope: [0,15]
	DischargeCutOffSOC = Register{Name: "Discharge cut-off SOC", Addr: 42003, Kind: U16, Gain: 1, Unit: "%", Access: RW, Min: 0, Max: 15, Bounded: true}

	// ChargingConfirmation is point-table row 161, "Charging confirmation" at 42207.
	// Scope: 0:Not allowed, 1:Allows
	ChargingConfirmation = Register{Name: "Charging confirmation", Addr: 42207, Kind: U16, Gain: 1, Unit: "", Access: WO}

	// GridCode is point-table row 162, "Grid code" at 42921.
	// Scope: For detailed definition of scope, please refer to the "Grid
	// Standard Code"
	GridCode = Register{Name: "Grid code", Addr: 42921, Kind: U16, Gain: 1, Unit: "", Access: RW}

	// WorkingMode is point-table row 163, "Working mode" at 43133.
	// Scope: 0: PQ 1: VSG
	WorkingMode = Register{Name: "Working mode", Addr: 43133, Kind: U16, Gain: 1, Unit: "", Access: RW}

	// ActivePowerPercent is point-table row 164, "Active power (%)[high]" at 42913.
	// Scope: [-100.00,100.00]
	ActivePowerPercent = Register{Name: "Active power (%)[high]", Addr: 42913, Kind: I16, Gain: 100, Unit: "%", Access: RW, Min: -100, Max: 100, Bounded: true}

	// ReactivePowerCompensation is point-table row 165, "Reactive power compensation (Q/S)" at 42914.
	// Scope: [-100.00,100.00]
	ReactivePowerCompensation = Register{Name: "Reactive power compensation (Q/S)", Addr: 42914, Kind: I16, Gain: 100, Unit: "%", Access: RW, Min: -100, Max: 100, Bounded: true}

	// ActivePowerSetpoint is point-table row 166, "Active power (kW)" at 42915.
	// Scope: [Rpmax, Pmax]
	ActivePowerSetpoint = Register{Name: "Active power (kW)", Addr: 42915, Kind: I32, Gain: 1000, Unit: "kW", Access: RW}

	// PowerFactorSetpoint is point-table row 167, "Power factor" at 42917.
	// Scope: (-1.000,-0.800]∪ [0.800,1.000]
	PowerFactorSetpoint = Register{Name: "Power factor", Addr: 42917, Kind: I16, Gain: 1000, Unit: "", Access: RW}

	// ActivePowerBaseline is point-table row 168, "Active power baseline" at 42936.
	// Scope: As a benchmark for active scheduling (percentage)
	ActivePowerBaseline = Register{Name: "Active power baseline", Addr: 42936, Kind: U32, Gain: 1000, Unit: "kW", Access: RW}
)

// PackCount is the number of battery packs the per-pack registers cover
// (rows 152-154, "Pack1-4").
const PackCount = 4

// packStride is the address distance between consecutive packs: rows 152-154
// list 33662/33862/34062/34262, 33663/33863/... and so on.
const packStride = 200

// PackVoltage is row 152, "Voltage of Pack(Pack1-4)", for pack 1..PackCount.
// It panics for a pack outside that range, which can only be a programming error.
func PackVoltage(pack int) Register { return packReg("Voltage of Pack", 33662, "V", 10, pack) }

// PackSOC is row 153, "SOC(Pack1-4)", for pack 1..PackCount.
func PackSOC(pack int) Register { return packReg("SOC of Pack", 33663, "%", 1, pack) }

// PackSOH is row 154, "SOH(Pack1-4)", for pack 1..PackCount.
func PackSOH(pack int) Register { return packReg("SOH of Pack", 33664, "%", 1, pack) }

func packReg(name string, base uint16, unit string, gain, pack int) Register {
	if pack < 1 || pack > PackCount {
		panic(fmt.Sprintf("huawei: pack %d out of range [1,%d]", pack, PackCount))
	}
	return Register{
		Name:   fmt.Sprintf("%s %d", name, pack),
		Addr:   base + uint16((pack-1)*packStride),
		Kind:   U16,
		Gain:   gain,
		Unit:   unit,
		Access: RO,
	}
}

// AlarmWords holds rows 9-20, 24-31 and 61-92: the 52 [Teleindication] alarm
// bitfields, in point-table order, so AlarmWords[0] is "Alarm 1". Their bit
// meanings are table 3-2, which this file does not carry.
var AlarmWords = [52]Register{
	{Name: "[Teleindication] Alarm 1", Addr: 30455, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 2", Addr: 30456, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 3", Addr: 30457, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 4", Addr: 30458, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 5", Addr: 30459, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 6", Addr: 30460, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 7", Addr: 30461, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 8", Addr: 30462, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 9", Addr: 30463, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 10", Addr: 30464, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 11", Addr: 30465, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 12", Addr: 30466, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 13", Addr: 30510, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 14", Addr: 30511, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 15", Addr: 30512, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 16", Addr: 30513, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 17", Addr: 30514, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 18", Addr: 30515, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 19", Addr: 30516, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 20", Addr: 30517, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 21", Addr: 32133, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 22", Addr: 32134, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 23", Addr: 32135, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 24", Addr: 32136, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 25", Addr: 32137, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 26", Addr: 32138, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 27", Addr: 32139, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 28", Addr: 32140, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 29", Addr: 32141, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 30", Addr: 32142, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 31", Addr: 32143, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 32", Addr: 32144, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 33", Addr: 32145, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 34", Addr: 32146, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 35", Addr: 32147, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 36", Addr: 32148, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 37", Addr: 32149, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 38", Addr: 32150, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 39", Addr: 32151, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 40", Addr: 32152, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 41", Addr: 32153, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 42", Addr: 32154, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 43", Addr: 32155, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 44", Addr: 32156, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 45", Addr: 32157, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 46", Addr: 32158, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 47", Addr: 32159, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 48", Addr: 32160, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 49", Addr: 32161, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 50", Addr: 32162, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 51", Addr: 32163, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "[Teleindication] Alarm 52", Addr: 32164, Kind: Bits16, Gain: 1, Access: RO},
}

// GridCodeMasks holds rows 94-125: the 32 grid-code mask bitfields, in
// point-table order, so GridCodeMasks[0] is "Grid code mask 1".
var GridCodeMasks = [32]Register{
	{Name: "Grid code mask 1", Addr: 32893, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 2", Addr: 32894, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 3", Addr: 32895, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 4", Addr: 32896, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 5", Addr: 32897, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 6", Addr: 32898, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 7", Addr: 32899, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 8", Addr: 32900, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 9", Addr: 32901, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 10", Addr: 32902, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 11", Addr: 32903, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 12", Addr: 32904, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 13", Addr: 32905, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 14", Addr: 32906, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 15", Addr: 32907, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 16", Addr: 32908, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 17", Addr: 32909, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Power grid code mask 18", Addr: 32910, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 19", Addr: 32911, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 20", Addr: 32912, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 21", Addr: 32913, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 22", Addr: 32914, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 23", Addr: 32915, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Power grid code mask 24", Addr: 32916, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 25", Addr: 32917, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 26", Addr: 32918, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 27", Addr: 32919, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Power grid code mask 28", Addr: 32920, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 29", Addr: 32921, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 30", Addr: 32922, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 31", Addr: 32923, Kind: Bits16, Gain: 1, Access: RO},
	{Name: "Grid code mask 32", Addr: 32924, Kind: Bits16, Gain: 1, Access: RO},
}

// Values of WorkStatus (row 45). The high byte groups the states: 0x00 idle,
// 0x01 starting, 0x02 running, 0x03 stopped, 0x04 spot check.
const (
	WorkIdle                   uint16 = 0x0000
	WorkLaunch                 uint16 = 0x0100
	WorkRunningPQ              uint16 = 0x0200
	WorkRunningVSG             uint16 = 0x0201
	WorkRunningHotSpare        uint16 = 0x0202
	WorkRunningLimitedPower    uint16 = 0x0204
	WorkRunningModuleDerating  uint16 = 0x0205
	WorkRunningBatteryCurLimit uint16 = 0x0206
	WorkRunningESSDerating     uint16 = 0x0207
	WorkRunningLatching        uint16 = 0x0208
	WorkAbnormalShutdown       uint16 = 0x0300
	WorkCommandShutdown        uint16 = 0x0301
	WorkPowerOnNotAuthorized   uint16 = 0x0302
	WorkPowerOff               uint16 = 0x0303
	WorkEPOOff                 uint16 = 0x0304
	WorkACOff                  uint16 = 0x0305
	WorkReadyForSpotCheck      uint16 = 0x0400
	WorkSpotCheckInProgress    uint16 = 0x0401
	WorkOffline                uint16 = 0xB000
	WorkLoading                uint16 = 0xC000
)

// Running reports whether a WorkStatus value is one of the 0x02xx running
// states, meaning the ESS is able to follow a power setpoint.
func Running(workStatus uint16) bool { return workStatus>>8 == 0x02 }

// WorkStatusName returns the documented name of a WorkStatus value, or a
// hex rendering for a value the point table does not define.
func WorkStatusName(v uint16) string {
	if s, ok := workStatusNames[v]; ok {
		return s
	}
	return fmt.Sprintf("unknown(0x%04X)", v)
}

var workStatusNames = map[uint16]string{
	WorkIdle: "Idle", WorkLaunch: "Launch",
	WorkRunningPQ: "Running: PQ", WorkRunningVSG: "Running: VSG",
	WorkRunningHotSpare: "Running: hot spare", WorkRunningLimitedPower: "Running: limited power",
	WorkRunningModuleDerating:  "Running: power module derating",
	WorkRunningBatteryCurLimit: "Running: battery current limit",
	WorkRunningESSDerating:     "Running: ESS derating", WorkRunningLatching: "Running: latching",
	WorkAbnormalShutdown: "Abnormal shutdown", WorkCommandShutdown: "Command shutdown",
	WorkPowerOnNotAuthorized: "Power-on not authorized", WorkPowerOff: "Power off",
	WorkEPOOff: "EPO off", WorkACOff: "AC off",
	WorkReadyForSpotCheck: "Ready for spot check", WorkSpotCheckInProgress: "Spot check in progress",
	WorkOffline: "Offline", WorkLoading: "Loading",
}

// Values of ChargingStatus (row 52).
const (
	ChargingIdle            uint16 = 0
	ChargingRechargeRequest uint16 = 1
	ChargingRecharging      uint16 = 2
	ChargingEnded           uint16 = 3
)

// Values of LTMSWorkingStatus (row 39), the liquid thermal management system.
const (
	LTMSOff             uint16 = 0
	LTMSSelfCirculating uint16 = 1
	LTMSRefrigeration   uint16 = 2
	LTMSHeating         uint16 = 3
)

// Values written to PowerOnOff (row 158). Note these are 1 and 2, not 0 and 1.
const (
	PowerStateRun uint16 = 1
	PowerStateOff uint16 = 2
)

// Values of WorkingMode (row 163): grid-following versus grid-forming. This is
// not an equivalent of the Sonnen auto/manual operating mode.
const (
	ModePQ  uint16 = 0
	ModeVSG uint16 = 1
)

// Values of ChargingConfirmation (row 161).
const (
	ChargingNotAllowed uint16 = 0
	ChargingAllowed    uint16 = 1
)

// Values of DaylightSavingEnable (row 157).
const (
	DSTDisable uint16 = 0
	DSTEnable  uint16 = 1
)
