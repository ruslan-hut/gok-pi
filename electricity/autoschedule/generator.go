package autoschedule

import (
	"fmt"
	"strings"

	"gok-pi/battery/entity"
	"gok-pi/electricity/pricefetcher"
	"gok-pi/electricity/scheduler"
)

const schedulePrefix = "auto-"

// GenerateSchedules creates charge/discharge schedules from price data
// for batteries that have AutoSchedule enabled.
func GenerateSchedules(batteries []entity.BatteryConfig, today, tomorrow *pricefetcher.DayData) []entity.Schedule {
	var schedules []entity.Schedule

	for _, bat := range batteries {
		if !bat.AutoSchedule || !bat.Enabled {
			continue
		}
		if today != nil {
			schedules = append(schedules, windowsToSchedules(bat, today.Schedule)...)
		}
		if tomorrow != nil {
			schedules = append(schedules, windowsToSchedules(bat, tomorrow.Schedule)...)
		}
	}

	return schedules
}

// IsAutoSchedule returns true if the schedule name starts with the auto prefix.
func IsAutoSchedule(name string) bool {
	return strings.HasPrefix(name, schedulePrefix)
}

func windowsToSchedules(bat entity.BatteryConfig, sched scheduler.DaySchedule) []entity.Schedule {
	var out []entity.Schedule

	for _, w := range sched.ChargeWindows {
		powerLimit := bat.PowerLimit
		if powerLimit <= 0 {
			powerLimit = 2000
		}
		out = append(out, entity.Schedule{
			Name:        fmt.Sprintf("%scharge-%s-%02d-%02d", schedulePrefix, bat.Name, w.StartHour, w.EndHour),
			Type:        "charge",
			StartTime:   fmt.Sprintf("%02d:00", w.StartHour),
			StopTime:    fmt.Sprintf("%02d:00", w.EndHour),
			BatteryName: bat.Name,
			Enabled:     true,
			PowerLimit:  powerLimit,
			SocLimit:    100,
		})
	}

	for _, w := range sched.DischargeWindows {
		socLimit := bat.SocLimit
		if socLimit <= 0 {
			socLimit = 10
		}
		powerLimit := bat.PowerLimit
		if powerLimit <= 0 {
			powerLimit = 2000
		}
		out = append(out, entity.Schedule{
			Name:        fmt.Sprintf("%sdischarge-%s-%02d-%02d", schedulePrefix, bat.Name, w.StartHour, w.EndHour),
			Type:        "discharge",
			StartTime:   fmt.Sprintf("%02d:00", w.StartHour),
			StopTime:    fmt.Sprintf("%02d:00", w.EndHour),
			BatteryName: bat.Name,
			Enabled:     true,
			PowerLimit:  powerLimit,
			SocLimit:    socLimit,
		})
	}

	return out
}
