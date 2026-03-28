// Package autoschedule converts electricity price analysis into concrete
// battery charge/discharge schedules.
//
// Two modes of operation:
//
//  1. DB-backed (preferred): BuildComputedSchedules() creates records for the DB,
//     ToLegacySchedules() converts DB records to entity.Schedule with stable ID-based names
//     (e.g., "auto-42-charge-battery1"). Only today's schedules are pushed to agents.
//
//  2. In-memory fallback: GenerateSchedules() works without DB, using time-based names
//     (e.g., "auto-charge-battery1-09-12"). Used when DB is unavailable.
//
// Auto-schedules are valid for exactly one day (00:00–23:59, no cross-midnight windows).
// Only batteries with AutoSchedule enabled in their config will get auto-schedules.
package autoschedule

import (
	"fmt"
	"time"

	"gok-pi/battery/entity"
	"gok-pi/electricity/pricefetcher"
	"gok-pi/electricity/scheduler"
	"gok-pi/remote/server/sessiondb"
)

const schedulePrefix = "auto-" // Prefix for auto-generated schedule names

// BuildComputedSchedules converts a day's price schedule into ComputedSchedule records
// for storage in the database. Each record represents a single charge or discharge window
// for one battery on the given day.
//
// Only batteries with AutoSchedule && Enabled are processed.
// Power/SoC limits are taken from battery config with defaults (2000W, 100% charge / 10% discharge).
func BuildComputedSchedules(batteries []entity.BatteryConfig, dayData *pricefetcher.DayData) []sessiondb.ComputedSchedule {
	if dayData == nil {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var out []sessiondb.ComputedSchedule

	for _, bat := range batteries {
		if !bat.AutoSchedule || !bat.Enabled {
			continue
		}

		// Charge windows: buy at prices ≤ P20
		for _, w := range dayData.Schedule.ChargeWindows {
			powerLimit := bat.PowerLimit
			if powerLimit <= 0 {
				powerLimit = 2000
			}
			out = append(out, sessiondb.ComputedSchedule{
				Date:        dayData.Date,
				BatteryName: bat.Name,
				Type:        "charge",
				StartHour:   w.StartHour,
				EndHour:     w.EndHour,
				AvgPrice:    w.AvgPrice,
				PowerLimit:  powerLimit,
				SocLimit:    100,
				Low:         dayData.Stats.Low,
				High:         dayData.Stats.High,
				ComputedAt:  now,
			})
		}

		// Discharge windows: sell at prices ≥ P80
		for _, w := range dayData.Schedule.DischargeWindows {
			socLimit := bat.SocLimit
			if socLimit <= 0 {
				socLimit = 10
			}
			powerLimit := bat.PowerLimit
			if powerLimit <= 0 {
				powerLimit = 2000
			}
			out = append(out, sessiondb.ComputedSchedule{
				Date:        dayData.Date,
				BatteryName: bat.Name,
				Type:        "discharge",
				StartHour:   w.StartHour,
				EndHour:     w.EndHour,
				AvgPrice:    w.AvgPrice,
				PowerLimit:  powerLimit,
				SocLimit:    socLimit,
				Low:         dayData.Stats.Low,
				High:         dayData.Stats.High,
				ComputedAt:  now,
			})
		}
	}

	return out
}

// ToLegacySchedules converts DB-backed computed schedules to entity.Schedule objects
// that agents understand. The name format is "auto-{id}-{type}-{battery}" where {id}
// is the stable database row ID, ensuring no name collisions across days.
//
// Expired windows (EndHour already passed for today) are skipped.
// chargePriceLimit and dischargePriceLimit filter windows by average price (0 = no limit).
func ToLegacySchedules(records []sessiondb.ComputedSchedule, now time.Time, chargePriceLimit, dischargePriceLimit float64) []entity.Schedule {
	var out []entity.Schedule

	for _, r := range records {
		// Skip windows whose end hour has already passed today
		stopTime := time.Date(now.Year(), now.Month(), now.Day(), r.EndHour, 0, 0, 0, now.Location())
		if now.After(stopTime) {
			continue
		}

		// Apply price limits: skip charge windows that are too expensive
		if r.Type == "charge" && chargePriceLimit > 0 && r.AvgPrice > chargePriceLimit {
			continue
		}
		// Apply price limits: skip discharge windows where price is too low
		if r.Type == "discharge" && dischargePriceLimit > 0 && r.AvgPrice < dischargePriceLimit {
			continue
		}

		out = append(out, entity.Schedule{
			// Name includes DB ID for guaranteed uniqueness across days.
			// Format: auto-{id}-{type}-{battery}
			// Example: auto-42-charge-battery1
			Name:        fmt.Sprintf("%s%d-%s-%s", schedulePrefix, r.ID, r.Type, r.BatteryName),
			Type:        r.Type,
			StartTime:   formatHour(r.StartHour),
			StopTime:    formatHour(r.EndHour),
			BatteryName: r.BatteryName,
			Enabled:     true,
			PowerLimit:  r.PowerLimit,
			SocLimit:    r.SocLimit,
		})
	}
	return out
}

// GenerateSchedules creates charge/discharge schedules from price data (in-memory fallback).
// Used when the database is unavailable. Names use the old format: "auto-{type}-{battery}-{HH}-{HH}".
// chargePriceLimit and dischargePriceLimit filter windows by average price (0 = no limit).
func GenerateSchedules(batteries []entity.BatteryConfig, today, tomorrow *pricefetcher.DayData, chargePriceLimit, dischargePriceLimit float64) []entity.Schedule {
	var schedules []entity.Schedule
	seen := make(map[string]bool)
	now := time.Now()

	for _, bat := range batteries {
		if !bat.AutoSchedule || !bat.Enabled {
			continue
		}
		// Today's schedules: skip expired windows, dedup by name.
		if today != nil {
			filtered := applyPriceLimits(today.Schedule, chargePriceLimit, dischargePriceLimit)
			for _, s := range windowsToSchedules(bat, filtered) {
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
			filtered := applyPriceLimits(tomorrow.Schedule, chargePriceLimit, dischargePriceLimit)
			for _, s := range windowsToSchedules(bat, filtered) {
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
	return entity.IsAutoSchedule(name)
}

// formatHour converts an hour (0-24) to a valid HH:MM string for entity.Schedule.
// Edge cases: hour 0 → "00:01" (midnight start), hour 24 → "23:59" (end of day).
// The entity.Schedule.Validate() requires valid HH:MM format where HH is 00-23,
// so 24:00 is not representable — we use 23:59 as the closest valid value.
func formatHour(hour int) string {
	switch hour {
	case 0:
		return "00:01"
	case 24:
		return "23:59"
	default:
		return fmt.Sprintf("%02d:00", hour)
	}
}

// applyPriceLimits returns a copy of the schedule with windows filtered by price limits.
func applyPriceLimits(sched scheduler.DaySchedule, chargePriceLimit, dischargePriceLimit float64) scheduler.DaySchedule {
	return scheduler.DaySchedule{
		ChargeWindows:    filterWindows(sched.ChargeWindows, chargePriceLimit, true),
		DischargeWindows: filterWindows(sched.DischargeWindows, dischargePriceLimit, false),
	}
}

// filterWindows removes windows that don't meet the price limit.
// For charge: skip if avgPrice > limit. For discharge: skip if avgPrice < limit.
// A limit of 0 means no filtering.
func filterWindows(windows []scheduler.Window, limit float64, isCharge bool) []scheduler.Window {
	if limit <= 0 {
		return windows
	}
	var out []scheduler.Window
	for _, w := range windows {
		if isCharge && w.AvgPrice > limit {
			continue
		}
		if !isCharge && w.AvgPrice < limit {
			continue
		}
		out = append(out, w)
	}
	return out
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
			StartTime:   formatHour(w.StartHour),
			StopTime:    formatHour(w.EndHour),
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
			StartTime:   formatHour(w.StartHour),
			StopTime:    formatHour(w.EndHour),
			BatteryName: bat.Name,
			Enabled:     true,
			PowerLimit:  powerLimit,
			SocLimit:    socLimit,
		})
	}

	return out
}
