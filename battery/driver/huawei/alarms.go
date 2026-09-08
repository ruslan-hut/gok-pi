package huawei

import "sort"

// Alarm definitions, transcribed from "LUNA2000B ESS Modbus Port Definitions",
// issue 01 (2025-09-10), table 3-2. Each alarm is one bit of one of the
// Bitfield16 registers listed in AlarmWords: reading those words and testing
// the bits named here is what turns the ESS's teleindication registers into
// something reportable.
//
// The table defines 207 alarms across 26 of the 52 alarm words; the remaining 26
// words carry no documented bits in this issue of the document. Entries the
// vendor named "Reserved" are kept so the bit is accounted for rather than
// silently ignored; Alarm.Reserved reports them.

// Alarm is one row of table 3-2: a fault identified by the vendor's alarm ID,
// raised while Bit of the register at Addr is set.
type Alarm struct {
	ID   uint16
	Name string
	Addr uint16
	Bit  uint8
}

// Reserved reports whether the vendor left this bit undefined.
func (a Alarm) Reserved() bool { return a.Name == "Reserved" }

// String implements fmt.Stringer.
func (a Alarm) String() string { return a.Name }

// Alarms lists every alarm of table 3-2, ordered by alarm ID.
var Alarms = [207]Alarm{
	{ID: 3100, Name: "Battery Pack and External Power Connected Abnormally", Addr: 32133, Bit: 0},
	{ID: 3101, Name: "Battery Pack Lifespan Reached", Addr: 32133, Bit: 1},
	{ID: 3102, Name: "Battery Pack Overvoltage", Addr: 32133, Bit: 2},
	{ID: 3103, Name: "Battery Pack Undervoltage", Addr: 32133, Bit: 3},
	{ID: 3104, Name: "Battery Overcurrent Protection", Addr: 32133, Bit: 4},
	{ID: 3105, Name: "Battery Pack Overtemperature", Addr: 32133, Bit: 5},
	{ID: 3106, Name: "Battery Pack Undertemperature", Addr: 32133, Bit: 6},
	{ID: 3107, Name: "Battery Pack Locked", Addr: 32133, Bit: 7},
	{ID: 3108, Name: "Battery Faulty", Addr: 32133, Bit: 8},
	{ID: 3109, Name: "Battery Pack SOH Low", Addr: 32133, Bit: 9},
	{ID: 3112, Name: "Power Connection Sampling of Battery Pack Abnormal", Addr: 32133, Bit: 12},
	{ID: 3113, Name: "Battery Pack Configuration Data Abnormal", Addr: 32133, Bit: 13},
	{ID: 3114, Name: "Battery Pack and System Specifications Mismatched", Addr: 32133, Bit: 14},
	{ID: 3115, Name: "Cell Temperature Rises Abnormally", Addr: 32133, Bit: 15},
	{ID: 3116, Name: "Pack Thermal Runaway", Addr: 32134, Bit: 0},
	{ID: 3117, Name: "Battery Pack Charging Failed", Addr: 32134, Bit: 1},
	{ID: 3161, Name: "Balancing Module Overcurrent Protection", Addr: 32136, Bit: 13},
	{ID: 3162, Name: "Balancing Module Internal Error", Addr: 32136, Bit: 14},
	{ID: 3163, Name: "Balancing Module Bus Voltage Abnormal", Addr: 32136, Bit: 15},
	{ID: 3164, Name: "Balancing Module Battery Voltage Abnormal", Addr: 32137, Bit: 0},
	{ID: 3165, Name: "Overtemperature Protection Inside the Balancing Module", Addr: 32137, Bit: 1},
	{ID: 3166, Name: "MCU Overtemperature Protection on the Battery Side of the Balancing Module", Addr: 32137, Bit: 2},
	{ID: 3167, Name: "Overtemperature Protection for the Wiring Terminal of the Balancing Module", Addr: 32137, Bit: 3},
	{ID: 3168, Name: "Reserved", Addr: 32137, Bit: 4},
	{ID: 3169, Name: "Balancing Module Bus Soft-Start Failed", Addr: 32137, Bit: 5},
	{ID: 3170, Name: "Balancing Module Version Mismatch", Addr: 32137, Bit: 6},
	{ID: 3171, Name: "Balancing Module Address Pairing Timeout", Addr: 32137, Bit: 7},
	{ID: 3172, Name: "Soft-Start MOS of Balancing Module Short-Circuited", Addr: 32137, Bit: 8},
	{ID: 3173, Name: "Balancing Module Abnormal", Addr: 32137, Bit: 9},
	{ID: 3174, Name: "Balancing Module Overcurrent Fault", Addr: 32137, Bit: 10},
	{ID: 3222, Name: "BMU Internal Short Circuit", Addr: 32140, Bit: 10},
	{ID: 3223, Name: "BMU Sampling Cable Disconnected", Addr: 32140, Bit: 11},
	{ID: 3224, Name: "BMU Faulty", Addr: 32140, Bit: 12},
	{ID: 3225, Name: "Reserved", Addr: 32140, Bit: 13},
	{ID: 3226, Name: "Disconnection Between BMUs", Addr: 32140, Bit: 14},
	{ID: 3227, Name: "Reserved", Addr: 32140, Bit: 15},
	{ID: 3228, Name: "Reserved", Addr: 32141, Bit: 0},
	{ID: 3229, Name: "BMU Communication Failure", Addr: 32141, Bit: 1},
	{ID: 3300, Name: "RPCB Voltage Abnormal", Addr: 32145, Bit: 8},
	{ID: 3301, Name: "RPCB Port Short-Circuited", Addr: 32145, Bit: 9},
	{ID: 3302, Name: "Internal RPCB Temperature Abnormal", Addr: 32145, Bit: 10},
	{ID: 3303, Name: "RPCB Overcurrent Fault", Addr: 32145, Bit: 11},
	{ID: 3304, Name: "Battery-Side ISO Insulation Detection Abnormal", Addr: 32145, Bit: 12},
	{ID: 3305, Name: "RPCB Power Loop Abnormal", Addr: 32145, Bit: 13},
	{ID: 3306, Name: "RPCB Version Mismatch", Addr: 32145, Bit: 14},
	{ID: 3307, Name: "RPCB Abnormal", Addr: 32145, Bit: 15},
	{ID: 3308, Name: "Battery Rack Residual Current Abnormal", Addr: 32146, Bit: 0},
	{ID: 3309, Name: "RPCB Overcurrent Protection Triggered", Addr: 32146, Bit: 1},
	{ID: 3310, Name: "RPCB Fan Alarm", Addr: 32146, Bit: 2},
	{ID: 3311, Name: "SPD Faulty", Addr: 32146, Bit: 3},
	{ID: 3312, Name: "Battery-Side ISO Insulation Detection Alarm", Addr: 32146, Bit: 4},
	{ID: 3313, Name: "Auxiliary Power Abnormal", Addr: 32146, Bit: 5},
	{ID: 3314, Name: "RPCB Overcurrent Alarm", Addr: 32146, Bit: 6},
	{ID: 3352, Name: "Abnormal Total Voltage of Battery Rack", Addr: 32148, Bit: 12},
	{ID: 3353, Name: "Total Voltage of Battery Rack and Total Cell Voltage Inconsistent", Addr: 32148, Bit: 13},
	{ID: 3354, Name: "Battery Pack Mixed Use Failed", Addr: 32148, Bit: 14},
	{ID: 3355, Name: "BCU Chip Overtemperature", Addr: 32148, Bit: 15},
	{ID: 3356, Name: "BCU Internal Exception", Addr: 32149, Bit: 0},
	{ID: 3357, Name: "Balancing Module Software Versions Inconsistent", Addr: 32149, Bit: 1},
	{ID: 3358, Name: "BCU Auxiliary Power Abnormal", Addr: 32149, Bit: 2},
	{ID: 3359, Name: "BCU Memory Abnormal", Addr: 32149, Bit: 3},
	{ID: 3360, Name: "RPCB Communication Failure", Addr: 32149, Bit: 4},
	{ID: 3361, Name: "DCDC Communication Failure", Addr: 32149, Bit: 5},
	{ID: 3362, Name: "PCS Communication Failure", Addr: 32149, Bit: 6},
	{ID: 3363, Name: "Balancing Module Communication Failure", Addr: 32149, Bit: 7},
	{ID: 3369, Name: "ESS SOH Calibration", Addr: 32149, Bit: 13},
	{ID: 3370, Name: "Reserved", Addr: 32149, Bit: 14},
	{ID: 3371, Name: "Battery Rack Voltage Exceeding Threshold", Addr: 32149, Bit: 15},
	{ID: 3372, Name: "Reserved", Addr: 32150, Bit: 0},
	{ID: 3373, Name: "Battery Pack Sampling Abnormal", Addr: 32150, Bit: 1},
	{ID: 3374, Name: "Reserved", Addr: 32150, Bit: 2},
	{ID: 3375, Name: "Battery Voltage Inconsistent", Addr: 32150, Bit: 3},
	{ID: 3376, Name: "Battery Temperature Inconsistent", Addr: 32150, Bit: 4},
	{ID: 3400, Name: "DCDC Protection", Addr: 32151, Bit: 12},
	{ID: 3401, Name: "DCDC Faulty", Addr: 32151, Bit: 13},
	{ID: 3402, Name: "Overvoltage Protection for DCDC Bus Port", Addr: 32151, Bit: 14},
	{ID: 3403, Name: "DCDC Overhumidity Protection", Addr: 32151, Bit: 15},
	{ID: 3404, Name: "DCDC Overtemperature Protection", Addr: 32152, Bit: 0},
	{ID: 3405, Name: "DCDC Undertemperature Protection", Addr: 32152, Bit: 1},
	{ID: 3406, Name: "Overvoltage Protection for DCDC Bus Terminal", Addr: 32152, Bit: 2},
	{ID: 3407, Name: "Overtemperature Protection for DCDC Battery Terminal", Addr: 32152, Bit: 3},
	{ID: 3408, Name: "DCDC Liquid Cooling Abnormal", Addr: 32152, Bit: 4},
	{ID: 3409, Name: "DCDC Versions Mismatched", Addr: 32152, Bit: 5},
	{ID: 3410, Name: "DCDC NTC Faulty", Addr: 32152, Bit: 6},
	{ID: 3411, Name: "DCDC DSP_EPO Faulty", Addr: 32152, Bit: 7},
	{ID: 3412, Name: "DCDC Short Circuit Protection", Addr: 32152, Bit: 8},
	{ID: 3413, Name: "DCDC Bus Overvoltage", Addr: 32152, Bit: 9},
	{ID: 3500, Name: "PCS DC Overvoltage", Addr: 32158, Bit: 0},
	{ID: 3501, Name: "PCS DC Bus in Reverse Polarity", Addr: 32158, Bit: 1},
	{ID: 3502, Name: "Reserved", Addr: 32158, Bit: 2},
	{ID: 3503, Name: "PCS Grid Phase Wire Short-Circuited to PE", Addr: 32158, Bit: 3},
	{ID: 3504, Name: "PCS Grid Failed", Addr: 32158, Bit: 4},
	{ID: 3505, Name: "PCS Grid Undervoltage", Addr: 32158, Bit: 5},
	{ID: 3506, Name: "PCS Grid Overvoltage", Addr: 32158, Bit: 6},
	{ID: 3507, Name: "PCS Grid Voltage Imbalanced", Addr: 32158, Bit: 7},
	{ID: 3508, Name: "PCS Grid Overfrequency", Addr: 32158, Bit: 8},
	{ID: 3509, Name: "PCS Grid Underfrequency", Addr: 32158, Bit: 9},
	{ID: 3510, Name: "PCS Grid Frequency Unstable", Addr: 32158, Bit: 10},
	{ID: 3511, Name: "PCS AC Overcurrent", Addr: 32158, Bit: 11},
	{ID: 3512, Name: "PCS DC Component Overhigh", Addr: 32158, Bit: 12},
	{ID: 3513, Name: "Reverse Phase Sequence on PCS AC Side", Addr: 32158, Bit: 13},
	{ID: 3514, Name: "PCS Residual Current Abnormal", Addr: 32158, Bit: 14},
	{ID: 3515, Name: "PCS Grounding Abnormal", Addr: 32158, Bit: 15},
	{ID: 3516, Name: "Low PCS Insulation Resistance", Addr: 32159, Bit: 0},
	{ID: 3517, Name: "PCS Temperature High", Addr: 32159, Bit: 1},
	{ID: 3518, Name: "PCS Abnormal", Addr: 32159, Bit: 2},
	{ID: 3519, Name: "PCS Update Failed or Versions Mismatched", Addr: 32159, Bit: 3},
	{ID: 3520, Name: "PCS Internal Fan Abnormal", Addr: 32159, Bit: 4},
	{ID: 3521, Name: "PCS AC Terminal Temperature Abnormal", Addr: 32159, Bit: 5},
	{ID: 3522, Name: "PCS DC Terminal Temperature Abnormal", Addr: 32159, Bit: 6},
	{ID: 3523, Name: "PCS Black Start Failed", Addr: 32159, Bit: 7},
	{ID: 3524, Name: "Incorrect Black Start Instruction Sequence of PCS", Addr: 32159, Bit: 8},
	{ID: 3525, Name: "PCS Fuse Broken", Addr: 32159, Bit: 9},
	{ID: 3526, Name: "PCS Fuse Self-Check Abnormal", Addr: 32159, Bit: 10},
	{ID: 3527, Name: "PCS FAST I/O Self-Test Abnormal", Addr: 32159, Bit: 11},
	{ID: 3528, Name: "PCS DC Bus Short-Circuited", Addr: 32159, Bit: 12},
	{ID: 3529, Name: "PCS Relay Overtemperature", Addr: 32159, Bit: 13},
	{ID: 3530, Name: "PCS AC Resonance", Addr: 32159, Bit: 14},
	{ID: 3531, Name: "PCS Derated", Addr: 32159, Bit: 15},
	{ID: 3600, Name: "Power loss alarm", Addr: 30455, Bit: 0},
	{ID: 3601, Name: "Power voltage abnormal", Addr: 30455, Bit: 1},
	{ID: 3602, Name: "Power frequency abnormal", Addr: 30455, Bit: 2},
	{ID: 3603, Name: "Outdoor temperature sensor fault", Addr: 30455, Bit: 3},
	{ID: 3604, Name: "Outdoor low temperature alarm", Addr: 30455, Bit: 4},
	{ID: 3605, Name: "LTMS communication abnormal", Addr: 30455, Bit: 5},
	{ID: 3606, Name: "LTMS expiration alarm", Addr: 30455, Bit: 6},
	{ID: 3607, Name: "Reserved", Addr: 30455, Bit: 7},
	{ID: 3608, Name: "Certificate about to expire", Addr: 30455, Bit: 8},
	{ID: 3609, Name: "Certificate has expired", Addr: 30455, Bit: 9},
	{ID: 3620, Name: "Compressor discharge high pressure alarm", Addr: 30456, Bit: 4},
	{ID: 3621, Name: "Compressor suction low pressure alarm", Addr: 30456, Bit: 5},
	{ID: 3622, Name: "Compressor low superheat degree alarm", Addr: 30456, Bit: 6},
	{ID: 3623, Name: "Compressor discharge pressure sensor fault", Addr: 30456, Bit: 7},
	{ID: 3624, Name: "Condenser outlet pressure sensor fault", Addr: 30456, Bit: 8},
	{ID: 3625, Name: "Condenser outlet temperature sensor fault", Addr: 30456, Bit: 9},
	{ID: 3626, Name: "Compressor suction pressure sensor fault", Addr: 30456, Bit: 10},
	{ID: 3627, Name: "Compressor suction temperature sensor fault", Addr: 30456, Bit: 11},
	{ID: 3628, Name: "Dehumidifying temperature sensor fault", Addr: 30456, Bit: 12},
	{ID: 3640, Name: "Compressor drive alarm", Addr: 30457, Bit: 8},
	{ID: 3641, Name: "Compressor drive output abnormal", Addr: 30457, Bit: 9},
	{ID: 3642, Name: "Compressor overcurrent alarm", Addr: 30457, Bit: 10},
	{ID: 3643, Name: "Compressor drive communication abnormal", Addr: 30457, Bit: 11},
	{ID: 3644, Name: "High discharge temperature alarm", Addr: 30457, Bit: 12},
	{ID: 3645, Name: "Compressor Discharge Temperature Sensor Faulty", Addr: 30457, Bit: 13},
	{ID: 3646, Name: "Insufficient Refrigerant", Addr: 30457, Bit: 14},
	{ID: 3647, Name: "Reserved", Addr: 30457, Bit: 15},
	{ID: 3650, Name: "Insufficient Cooling Capacity", Addr: 30458, Bit: 2},
	{ID: 3655, Name: "Auxiliary power abnormal", Addr: 30458, Bit: 7},
	{ID: 3660, Name: "Outdoor cooling module blocked", Addr: 30458, Bit: 12},
	{ID: 3661, Name: "Outdoor heat exchanger temperature sensor fault", Addr: 30458, Bit: 13},
	{ID: 3664, Name: "Reserved", Addr: 30459, Bit: 0},
	{ID: 3665, Name: "Outdoor fan fault", Addr: 30459, Bit: 1},
	{ID: 3666, Name: "Dehumidifier fan fault", Addr: 30459, Bit: 2},
	{ID: 3675, Name: "Electric heater fault", Addr: 30459, Bit: 11},
	{ID: 3676, Name: "Electric heater power overvoltage alarm", Addr: 30459, Bit: 12},
	{ID: 3680, Name: "Power-side supply water temperature sensor fault", Addr: 30460, Bit: 0},
	{ID: 3681, Name: "Power-side return water temperature sensor fault", Addr: 30460, Bit: 1},
	{ID: 3682, Name: "Power-side supply/return water temperature sensor abnormal", Addr: 30460, Bit: 2},
	{ID: 3683, Name: "Battery-side supply water temperature sensor fault", Addr: 30460, Bit: 3},
	{ID: 3684, Name: "Battery-side return water temperature sensor fault", Addr: 30460, Bit: 4},
	{ID: 3685, Name: "Battery-side supply/return water temperature sensor abnormal", Addr: 30460, Bit: 5},
	{ID: 3686, Name: "Battery-side supply water high temperature alarm", Addr: 30460, Bit: 6},
	{ID: 3687, Name: "Battery-side supply water low temperature alarm", Addr: 30460, Bit: 7},
	{ID: 3688, Name: "Coolant expiration alarm", Addr: 30460, Bit: 8},
	{ID: 3689, Name: "Shutdown due to coolant expiration", Addr: 30460, Bit: 9},
	{ID: 3690, Name: "Coolant replacement not completed", Addr: 30460, Bit: 10},
	{ID: 3705, Name: "Water pump power supply abnormal", Addr: 30461, Bit: 9},
	{ID: 3706, Name: "Water pump function abnormal", Addr: 30461, Bit: 10},
	{ID: 3707, Name: "Water pump fault", Addr: 30461, Bit: 11},
	{ID: 3715, Name: "Multi-way valve communication abnormal", Addr: 30462, Bit: 3},
	{ID: 3716, Name: "Multi-way valve power supply abnormal", Addr: 30462, Bit: 4},
	{ID: 3717, Name: "Multi-way valve faulty", Addr: 30462, Bit: 5},
	{ID: 3725, Name: "Water tank low liquid level alarm", Addr: 30462, Bit: 13},
	{ID: 3726, Name: "Reserved", Addr: 30462, Bit: 14},
	{ID: 3727, Name: "Reserved", Addr: 30462, Bit: 15},
	{ID: 3880, Name: "AC SPD Faulty", Addr: 30510, Bit: 8},
	{ID: 3881, Name: "Door Status Alarm", Addr: 30510, Bit: 9},
	{ID: 3882, Name: "ESS Door Open", Addr: 30510, Bit: 10},
	{ID: 3883, Name: "Water Alarm", Addr: 30510, Bit: 11},
	{ID: 3884, Name: "Smoke Detector Alarm", Addr: 30510, Bit: 12},
	{ID: 3885, Name: "High Concentration of Combustible Gas", Addr: 30510, Bit: 13},
	{ID: 3886, Name: "Combustible Gas Detector Communication Failed", Addr: 30510, Bit: 14},
	{ID: 3887, Name: "Combustible Gas Detector Faulty", Addr: 30510, Bit: 15},
	{ID: 3888, Name: "Temperature and Humidity Sensor Communication Failed", Addr: 30511, Bit: 0},
	{ID: 3889, Name: "Temperature and Humidity Sensor Faulty", Addr: 30511, Bit: 1},
	{ID: 3890, Name: "Heat Detector Alarm", Addr: 30511, Bit: 2},
	{ID: 3891, Name: "High Ambient Temperature Inside ESS Cabin", Addr: 30511, Bit: 3},
	{ID: 3892, Name: "EPO Alarm", Addr: 30511, Bit: 4},
	{ID: 3893, Name: "Fire Alarm", Addr: 30511, Bit: 5},
	{ID: 3894, Name: "Exhaust Fan Faulty", Addr: 30511, Bit: 6},
	{ID: 3895, Name: "Devices Connected and System Configuration Inconsistent", Addr: 30511, Bit: 7},
	{ID: 3898, Name: "TRSD Abnormal", Addr: 30511, Bit: 10},
	{ID: 3899, Name: "TRSD Valve Open", Addr: 30511, Bit: 11},
	{ID: 3900, Name: "High Relative Humidity Inside ESS Cabin", Addr: 30511, Bit: 12},
	{ID: 3901, Name: "Offering Software Update Package Not Backed Up", Addr: 30511, Bit: 13},
	{ID: 3902, Name: "Component Software Versions Inconsistent", Addr: 30511, Bit: 14},
	{ID: 3903, Name: "E-label Board Data Abnormal", Addr: 30511, Bit: 15},
	{ID: 3904, Name: "Certificate About to Expire", Addr: 30512, Bit: 0},
	{ID: 3905, Name: "Certificate Expired", Addr: 30512, Bit: 1},
	{ID: 3906, Name: "Communication with Upper-layer Controller Abnormal", Addr: 30512, Bit: 2},
	{ID: 3907, Name: "Reserved", Addr: 30512, Bit: 3},
	{ID: 3908, Name: "Sensor End of Life", Addr: 30512, Bit: 4},
	{ID: 3909, Name: "TRSD Communication Abnormal", Addr: 30512, Bit: 5},
	{ID: 3910, Name: "Auxiliary Power Meter Communication Abnormal", Addr: 30512, Bit: 6},
	{ID: 3911, Name: "Display Module Communication Failure", Addr: 30512, Bit: 7},
	{ID: 3912, Name: "Startup Authorization Not Obtained", Addr: 30512, Bit: 8},
	{ID: 3913, Name: "Fire Extinguishing Agents in TRSD Sprayed", Addr: 30512, Bit: 9},
}

// alarmsByAddr indexes Alarms by register address so a word read from the ESS
// can be decoded without scanning the whole table.
var alarmsByAddr = func() map[uint16][]Alarm {
	m := make(map[uint16][]Alarm, 32)
	for _, a := range Alarms {
		m[a.Addr] = append(m[a.Addr], a)
	}

	return m
}()

// alarmsByID indexes Alarms by the vendor's alarm ID.
var alarmsByID = func() map[uint16]Alarm {
	m := make(map[uint16]Alarm, len(Alarms))
	for _, a := range Alarms {
		m[a.ID] = a
	}

	return m
}()

// AlarmByID returns the alarm with the given vendor ID.
func AlarmByID(id uint16) (Alarm, bool) {
	a, ok := alarmsByID[id]

	return a, ok
}

// AlarmsInWord returns the alarms raised by word, the value read from the alarm
// register at addr, ordered by alarm ID. Reserved bits are included; filter
// them with Alarm.Reserved if they are noise. An address with no documented
// bits yields nothing.
func AlarmsInWord(addr, word uint16) []Alarm {
	var out []Alarm
	for _, a := range alarmsByAddr[addr] {
		if Bit(word, int(a.Bit)) {
			out = append(out, a)
		}
	}

	return out
}

// ActiveAlarms returns every alarm raised across a set of alarm words, ordered
// by alarm ID. The map keys are register addresses from AlarmWords and the
// values are the words read from them; addresses that are absent, or that carry
// no documented bits, contribute nothing.
func ActiveAlarms(words map[uint16]uint16) []Alarm {
	var out []Alarm
	for addr, word := range words {
		out = append(out, AlarmsInWord(addr, word)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out
}
