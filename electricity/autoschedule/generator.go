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
// and filters out today's windows whose end time has already passed.
// Tomorrow's windows are never filtered — only deduplicated.
func GenerateSchedules(batteries []entity.BatteryConfig, today, tomorrow *pricefetcher.DayData) []entity.Schedule {
	var schedules []entity.Schedule
	seen := make(map[string]bool)
	now := time.Now()

	for _, bat := range batteries {
		if !bat.AutoSchedule || !bat.Enabled {
			continue
		}
		// Today's schedules: skip expired windows, dedup by name.
		if today != nil {
			for _, s := range windowsToSchedules(bat, today.Schedule) {
				if seen[s.Name] {
					continue
				}
				var stopHour int
				if _, err := fmt.Sscanf(s.StopTime, "%d:", &stopHour); err == nil {
					stopTime := time.Date(now.Year(), now.Month(), now.Day(), stopHour, 0, 0, 0, now.Location())
					if now.After(stopTime) {
						continue
					}
				}
				seen[s.Name] = true
				schedules = append(schedules, s)
			}
		}
		// Tomorrow's schedules: dedup only (same name as a surviving today window is skipped).
		if tomorrow != nil {
			for _, s := range windowsToSchedules(bat, tomorrow.Schedule) {
				if seen[s.Name] {
					continue
				}
				seen[s.Name] = true
				schedules = append(schedules, s)
			}
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
