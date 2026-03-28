package entity

// CloneSchedules returns a shallow copy of the schedule slice.
func CloneSchedules(in []Schedule) []Schedule {
	if len(in) == 0 {
		return nil
	}
	out := make([]Schedule, len(in))
	copy(out, in)
	return out
}

// CloneBatteryConfigs returns a shallow copy of the battery config slice.
func CloneBatteryConfigs(in []BatteryConfig) []BatteryConfig {
	if len(in) == 0 {
		return nil
	}
	out := make([]BatteryConfig, len(in))
	copy(out, in)
	return out
}
