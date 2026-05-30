package entity

// CloneSchedules returns a deep copy of the schedule slice. The GoalReachedTime
// pointer is duplicated (not aliased) so callers can serialize or mutate the copy
// without racing the original.
func CloneSchedules(in []Schedule) []Schedule {
	if len(in) == 0 {
		return nil
	}
	out := make([]Schedule, len(in))
	copy(out, in)
	for i := range out {
		if in[i].GoalReachedTime != nil {
			t := *in[i].GoalReachedTime
			out[i].GoalReachedTime = &t
		}
	}
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
