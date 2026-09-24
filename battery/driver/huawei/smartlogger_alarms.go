package huawei

import "sort"

// LoggerAlarms is table 2-2 of the SmartLogger document, issue 54: the bits of
// LoggerAlarmWords, ordered by alarm ID and sub-ID. One alarm ID can cover
// several causes, each on its own bit, so SubID and Cause tell them apart.
//
// The table itself assigns two bits twice: 50005 bit 13 to both 1141-2 and
// 1142-5, and 50005 bit 14 to both 1141-3 and 1143-1. Both claimants are kept,
// so a set bit reports both and the ambiguity stays visible rather than being
// resolved by guesswork.
var LoggerAlarms = [90]Alarm{
	{ID: 1100, SubID: 4, Name: "Abnormal Active Schedule", Cause: "DI ports read an unconfigured combination", Addr: 50000, Bit: 3},
	{ID: 1100, SubID: 5, Name: "Abnormal Active Schedule", Cause: "no or abnormal remote dispatch commands", Addr: 50000, Bit: 4},
	{ID: 1101, SubID: 4, Name: "Abnormal Reactive Schedule", Cause: "DI ports read an unconfigured combination", Addr: 50000, Bit: 11},
	{ID: 1101, SubID: 5, Name: "Abnormal Reactive Schedule", Cause: "no or abnormal remote dispatch commands", Addr: 50000, Bit: 12},
	{ID: 1103, SubID: 1, Name: "MCB Disconnect", Addr: 50001, Bit: 1},
	{ID: 1104, SubID: 1, Name: "Abnormal Cubicle", Addr: 50001, Bit: 2},
	{ID: 1105, SubID: 1, Name: "Device Address Conflict", Addr: 50001, Bit: 3},
	{ID: 1106, SubID: 1, Name: "AC SPD Fault", Addr: 50001, Bit: 4},
	{ID: 1107, SubID: 1, Name: "DI1 Custom Alarm", Addr: 50001, Bit: 5},
	{ID: 1108, SubID: 1, Name: "DI2 Custom Alarm", Addr: 50001, Bit: 6},
	{ID: 1109, SubID: 1, Name: "DI3 Custom Alarm", Addr: 50001, Bit: 7},
	{ID: 1110, SubID: 1, Name: "DI4 Custom Alarm", Addr: 50001, Bit: 8},
	{ID: 1111, SubID: 1, Name: "DI5 Custom Alarm", Addr: 50001, Bit: 9},
	{ID: 1112, SubID: 1, Name: "DI6 Custom Alarm", Addr: 50001, Bit: 10},
	{ID: 1113, SubID: 1, Name: "DI7 Custom Alarm", Addr: 50001, Bit: 11},
	{ID: 1114, SubID: 1, Name: "DI8 Custom Alarm", Addr: 50001, Bit: 12},
	{ID: 1115, SubID: 1, Name: "24V Power Failure", Addr: 50001, Bit: 13},
	{ID: 1116, SubID: 1, Name: "WebUI Server Certificate Invalid", Addr: 50002, Bit: 0},
	{ID: 1117, SubID: 1, Name: "WebUI Server Certificate to Expire", Addr: 50002, Bit: 1},
	{ID: 1118, SubID: 1, Name: "WebUI Server Certificate Expired", Addr: 50002, Bit: 2},
	{ID: 1119, SubID: 1, Name: "License Expired", Addr: 50001, Bit: 14},
	{ID: 1120, SubID: 1, Name: "Management System Certificate Invalid", Addr: 50002, Bit: 3},
	{ID: 1120, SubID: 2, Name: "Management System Certificate Invalid", Cause: "management system 1 signature certificate", Addr: 50003, Bit: 0},
	{ID: 1120, SubID: 3, Name: "Management System Certificate Invalid", Cause: "SPPC signature certificate", Addr: 50003, Bit: 4},
	{ID: 1121, SubID: 1, Name: "Management System Certificate to Expire", Addr: 50002, Bit: 4},
	{ID: 1121, SubID: 2, Name: "Management System Certificate to Expire", Cause: "management system 1 signature certificate", Addr: 50003, Bit: 1},
	{ID: 1121, SubID: 3, Name: "Management System Certificate to Expire", Cause: "SPPC signature certificate", Addr: 50003, Bit: 5},
	{ID: 1122, SubID: 1, Name: "Management System Certificate Expired", Addr: 50002, Bit: 5},
	{ID: 1122, SubID: 2, Name: "Management System Certificate Expired", Cause: "management system 1 signature certificate", Addr: 50003, Bit: 2},
	{ID: 1122, SubID: 3, Name: "Management System Certificate Expired", Cause: "SPPC signature certificate", Addr: 50003, Bit: 6},
	{ID: 1123, SubID: 1, Name: "Remote Power Control Certificate Not Activated", Addr: 50002, Bit: 6},
	{ID: 1124, SubID: 1, Name: "Remote Power Control Certificate About to Expire", Addr: 50002, Bit: 7},
	{ID: 1125, SubID: 1, Name: "Remote Power Control Certificate Expired", Addr: 50002, Bit: 8},
	{ID: 1126, SubID: 1, Name: "Poverty Alleviation Monitoring Center Certificate Invalid", Addr: 50002, Bit: 9},
	{ID: 1127, SubID: 1, Name: "Poverty Alleviation Monitoring Center Certificate to Expire", Addr: 50002, Bit: 10},
	{ID: 1128, SubID: 1, Name: "Poverty Alleviation Monitoring Center Certificate Expired", Addr: 50002, Bit: 11},
	{ID: 1129, SubID: 1, Name: "SmartLogger Certificate Invalid", Addr: 50002, Bit: 12},
	{ID: 1130, SubID: 1, Name: "SmartLogger Certificate About to Expire", Addr: 50002, Bit: 13},
	{ID: 1131, SubID: 1, Name: "SmartLogger Certificate Expired", Addr: 50002, Bit: 14},
	{ID: 1132, SubID: 1, Name: "Smart Rack Controller Cables Not Connected to DC Bus", Addr: 50002, Bit: 15},
	{ID: 1133, SubID: 1, Name: "Smart Tracking Mount Out of Control", Addr: 50004, Bit: 0},
	{ID: 1134, SubID: 1, Name: "Smart PCS Cables Not Connected to DC Bus", Addr: 50003, Bit: 3},
	{ID: 1135, SubID: 1, Name: "SDS License Capacity Insufficient", Addr: 50004, Bit: 1},
	{ID: 1140, SubID: 1, Name: "Array Black Start Failed", Cause: "command out of sequence", Addr: 50005, Bit: 0},
	{ID: 1140, SubID: 2, Name: "Array Black Start Failed", Cause: "array state does not allow it", Addr: 50005, Bit: 1},
	{ID: 1140, SubID: 3, Name: "Array Black Start Failed", Cause: "no available ESS", Addr: 50005, Bit: 2},
	{ID: 1140, SubID: 4, Name: "Array Black Start Failed", Cause: "ESS does not support black start", Addr: 50005, Bit: 3},
	{ID: 1140, SubID: 5, Name: "Array Black Start Failed", Cause: "PCS does not support black start", Addr: 50005, Bit: 4},
	{ID: 1140, SubID: 6, Name: "Array Black Start Failed", Cause: "ESS black start failed", Addr: 50005, Bit: 5},
	{ID: 1140, SubID: 7, Name: "Array Black Start Failed", Cause: "no available PCS", Addr: 50005, Bit: 6},
	{ID: 1140, SubID: 8, Name: "Array Black Start Failed", Cause: "PCS black start failed", Addr: 50005, Bit: 7},
	{ID: 1141, SubID: 1, Name: "ESS Shutdown upon STS Switch-off", Cause: "STS switched off", Addr: 50005, Bit: 8},
	{ID: 1141, SubID: 2, Name: "ESS Shutdown upon STS Switch-off", Cause: "battery compartment EPO or serious fault", Addr: 50005, Bit: 13},
	{ID: 1141, SubID: 3, Name: "ESS Shutdown upon STS Switch-off", Cause: "logger disconnected from the BMS", Addr: 50005, Bit: 14},
	{ID: 1142, SubID: 1, Name: "Grid Connection Point Switch Control Failure", Cause: "dry contact trip failed", Addr: 50005, Bit: 9},
	{ID: 1142, SubID: 2, Name: "Grid Connection Point Switch Control Failure", Cause: "dry contact closing failed", Addr: 50005, Bit: 10},
	{ID: 1142, SubID: 3, Name: "Grid Connection Point Switch Control Failure", Cause: "relay protection trip failed", Addr: 50005, Bit: 11},
	{ID: 1142, SubID: 4, Name: "Grid Connection Point Switch Control Failure", Cause: "relay protection closing failed", Addr: 50005, Bit: 12},
	{ID: 1142, SubID: 5, Name: "Grid Connection Point Switch Control Failure", Cause: "third-party synchronization check failed", Addr: 50005, Bit: 13},
	{ID: 1143, SubID: 1, Name: "Abnormal ESS Insulation Resistance", Addr: 50005, Bit: 14},
	{ID: 1144, SubID: 1, Name: "Load Switch Control Failure", Addr: 50006, Bit: 0},
	{ID: 1144, SubID: 2, Name: "Load Switch Control Failure", Addr: 50006, Bit: 1},
	{ID: 1145, SubID: 1, Name: "D.G. Control Failure", Cause: "generator startup failed", Addr: 50005, Bit: 15},
	{ID: 1145, SubID: 2, Name: "D.G. Control Failure", Cause: "generator shutdown failed", Addr: 50006, Bit: 6},
	{ID: 1146, SubID: 1, Name: "Abnormal On/Off-Grid Switching Adjustment", Cause: "off-grid to grid synchronization failed", Addr: 50006, Bit: 2},
	{ID: 1146, SubID: 2, Name: "Abnormal On/Off-Grid Switching Adjustment", Cause: "islanding power regulation failed", Addr: 50006, Bit: 3},
	{ID: 1147, SubID: 1, Name: "System Black Start Failure", Addr: 50006, Bit: 4},
	{ID: 1148, SubID: 1, Name: "Power-off Due to PV Array Faults", Cause: "array shut down on lost PPC/NMS communication", Addr: 50006, Bit: 5},
	{ID: 1149, SubID: 1, Name: "Communication Cabinet Temperature Too High", Addr: 50006, Bit: 9},
	{ID: 1150, SubID: 1, Name: "Abnormal Power Control at the Grid Connection Point", Addr: 50006, Bit: 7},
	{ID: 1152, SubID: 1, Name: "Energy Storage Control Abnormality", Cause: "control mode does not match the ESS model", Addr: 50006, Bit: 8},
	{ID: 1154, SubID: 1, Name: "Abnormal Communication with Southbound Devices", Cause: "inverter", Addr: 50007, Bit: 5},
	{ID: 1154, SubID: 2, Name: "Abnormal Communication with Southbound Devices", Cause: "PCS", Addr: 50007, Bit: 6},
	{ID: 1154, SubID: 3, Name: "Abnormal Communication with Southbound Devices", Cause: "energy storage", Addr: 50007, Bit: 7},
	{ID: 1154, SubID: 4, Name: "Abnormal Communication with Southbound Devices", Cause: "module", Addr: 50007, Bit: 8},
	{ID: 1154, SubID: 5, Name: "Abnormal Communication with Southbound Devices", Cause: "meter", Addr: 50007, Bit: 9},
	{ID: 1154, SubID: 6, Name: "Abnormal Communication with Southbound Devices", Cause: "PID", Addr: 50007, Bit: 10},
	{ID: 1154, SubID: 7, Name: "Abnormal Communication with Southbound Devices", Cause: "STS", Addr: 50007, Bit: 11},
	{ID: 1154, SubID: 8, Name: "Abnormal Communication with Southbound Devices", Cause: "external MBUS", Addr: 50007, Bit: 12},
	{ID: 1154, SubID: 9, Name: "Abnormal Communication with Southbound Devices", Cause: "BMS", Addr: 50007, Bit: 13},
	{ID: 1160, SubID: 1, Name: "Expansion Module Software Version Mismatch", Addr: 50006, Bit: 10},
	{ID: 1161, SubID: 1, Name: "Battery Cluster Input Detection Failed", Addr: 50006, Bit: 11},
	{ID: 1162, SubID: 1, Name: "Battery Cluster Fast IO Detection Failed", Addr: 50006, Bit: 12},
	{ID: 1163, SubID: 1, Name: "PV Array Topology Abnormal", Cause: "ESS cable check failed or topology mismatch", Addr: 50007, Bit: 0},
	{ID: 1163, SubID: 2, Name: "PV Array Topology Abnormal", Cause: "DC bus parallel PCS count wrong or topology mismatch", Addr: 50007, Bit: 1},
	{ID: 1164, SubID: 1, Name: "ESS Control Abnormal", Cause: "battery compartment startup failure", Addr: 50007, Bit: 2},
	{ID: 1165, SubID: 1, Name: "Inconsistent PCS Parameters", Cause: "VSG parameters", Addr: 50007, Bit: 3},
	{ID: 1165, SubID: 2, Name: "Inconsistent PCS Parameters", Cause: "GFM parameters", Addr: 50007, Bit: 4},
	{ID: 1166, SubID: 1, Name: "SOC Equalization Failed", Addr: 50007, Bit: 14},
	{ID: 1167, SubID: 1, Name: "Wiring Sequence Abnormality", Cause: "inconsistent AC phase sequence within the subarray", Addr: 50007, Bit: 15},
}

var loggerAlarmsByAddr = func() map[uint16][]Alarm {
	m := make(map[uint16][]Alarm, len(LoggerAlarmWords))
	for _, a := range LoggerAlarms {
		m[a.Addr] = append(m[a.Addr], a)
	}

	return m
}()

// LoggerAlarmsInWord returns the logger alarms raised by word, the value read
// from the alarm register at addr, ordered by alarm ID and sub-ID.
func LoggerAlarmsInWord(addr, word uint16) []Alarm {
	var out []Alarm
	for _, a := range loggerAlarmsByAddr[addr] {
		if Bit(word, int(a.Bit)) {
			out = append(out, a)
		}
	}

	return out
}

// ActiveLoggerAlarms returns every logger alarm raised across a set of alarm
// words, keyed by register address, ordered by alarm ID and sub-ID.
func ActiveLoggerAlarms(words map[uint16]uint16) []Alarm {
	var out []Alarm
	for addr, word := range words {
		out = append(out, LoggerAlarmsInWord(addr, word)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}

		return out[i].SubID < out[j].SubID
	})

	return out
}
