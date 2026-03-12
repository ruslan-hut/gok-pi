// Package autoschedule converts electricity price analysis into concrete
// battery charge/discharge schedules.
//
// It takes the output of the scheduler package (cheapest/most expensive hour windows)
// and generates entity.Schedule objects named "auto-{type}-{battery}-{start}-{end}".
// These auto-schedules are pushed to agents via the control server and are automatically
// removed by the discharger/charger when their time window expires.
//
// Only batteries with AutoSchedule enabled in their config will get auto-schedules.
package autoschedule

import (
	"fmt"
	"strings"
	"time"

	"gok-pi/battery/entity"
	"gok-pi/electricity/pricefetcher"
	"gok-pi/electricity/scheduler"
)

const schedulePrefix = "auto-" // Prefix for auto-generated schedule names

// GenerateSchedules creates charge/discharge schedules from price data
// for batteries that have AutoSchedule enabled.
// It deduplicates by schedule name (today and tomorrow may produce identical windows)
// and filters out windows whose end time has already passed today.
func GenerateSchedules(batteries []entity.BatteryConfig, today, tomorrow *pricefetcher.DayData) []entity.Schedule {
	var schedules []entity.Schedule
	seen := make(map[string]bool)
	now := time.Now()

	for _, bat := range batteries {
		if !bat.AutoSchedule || !bat.Enabled {
			continue
		}
		if today != nil {
			schedules = appendUnique(schedules, windowsToSchedules(bat, today.Schedule), seen, now)
		}
		if tomorrow != nil {
			schedules = appendUnique(schedules, windowsToSchedules(bat, tomorrow.Schedule), seen, now)
		}
	}

	return schedules
}

// appendUnique adds schedules that haven't been seen yet and whose time window
// hasn't expired. A window is expired when current time is past its stop hour today.
func appendUnique(dst []entity.Schedule, src []entity.Schedule, seen map[string]bool, now time.Time) []entity.Schedule {
	for _, s := range src {
		if seen[s.Name] {
			continue
		}
		// Parse stop hour and skip if the window has already ended today.
		var stopHour int
		if _, err := fmt.Sscanf(s.StopTime, "%d:", &stopHour); err == nil {
			stopTime := time.Date(now.Year(), now.Month(), now.Day(), stopHour, 0, 0, 0, now.Location())
			if now.After(stopTime) {
				continue
			}
		}
		seen[s.Name] = true
		dst = append(dst, s)
	}
	return dst
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
