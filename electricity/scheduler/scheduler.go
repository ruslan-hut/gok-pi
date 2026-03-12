// Package scheduler analyzes hourly electricity prices to determine optimal
// charge and discharge time windows for battery systems.
//
// Algorithm — P25/P75 Percentile Strategy:
//
//  1. Sort all 24 hourly prices ascending
//  2. Compute P25 (25th percentile) — the price threshold below which to charge (buy)
//  3. Compute P75 (75th percentile) — the price threshold above which to discharge (sell)
//  4. Select all hours with price ≤ P25 as charge hours
//  5. Select all hours with price ≥ P75 as discharge hours
//  6. Merge adjacent hours into contiguous windows (e.g., hours 2,3,4 → window 02:00-05:00)
//
// This strategy is self-adaptive: thresholds recalculate daily based on actual
// price distribution from REE forecast. On flat-price days fewer hours qualify
// (avoiding unprofitable cycling), on volatile days more hours qualify at extremes
// (capturing more opportunity). The logic buys at the lower quartile and sells at
// the upper quartile, guaranteeing operations always occur at the extremes of the
// real price range for each day, regardless of absolute market level.
//
// The output DaySchedule is consumed by the autoschedule package to generate
// entity.Schedule objects that are pushed to agents via the control server.
package scheduler

import (
	"math"
	"sort"

	"gok-pi/electricity/redata"
)

// Window represents a contiguous time window for charging or discharging.
type Window struct {
	StartHour int     `json:"start_hour"`
	EndHour   int     `json:"end_hour"`
	AvgPrice  float64 `json:"avg_price_eur_mwh"`
}

// DaySchedule contains computed charge/discharge windows for a single day.
type DaySchedule struct {
	ChargeWindows    []Window `json:"charge_windows"`
	DischargeWindows []Window `json:"discharge_windows"`
}

// Stats holds price statistics for the day.
type Stats struct {
	MinPrice float64 `json:"min_price_eur_mwh"`
	MaxPrice float64 `json:"max_price_eur_mwh"`
	AvgPrice float64 `json:"avg_price_eur_mwh"`
	P25      float64 `json:"p25_eur_mwh"` // 25th percentile — charge threshold
	P75      float64 `json:"p75_eur_mwh"` // 75th percentile — discharge threshold
}

// ComputeSchedule analyzes hourly prices using a P25/P75 percentile strategy.
//
// How it works:
//   - P25 (25th percentile): 25% of hours have a price ≤ this value — these are
//     the cheap hours suitable for charging.
//   - P75 (75th percentile): only 25% of hours have a price ≥ this value — these
//     are the expensive hours suitable for discharging/selling.
//   - Hours with price ≤ P25 → charge windows (buy at the lower quartile)
//   - Hours with price ≥ P75 → discharge windows (sell at the upper quartile)
//
// This replaces the previous Top-N approach (fixed 3 cheapest / 3 most expensive)
// with a fully adaptive strategy where the number of active hours depends on the
// day's price distribution rather than being hardcoded.
func ComputeSchedule(prices []redata.HourlyPrice) DaySchedule {
	if len(prices) == 0 {
		return DaySchedule{}
	}

	// Sort prices ascending to compute percentiles
	sorted := make([]float64, len(prices))
	for i, p := range prices {
		sorted[i] = p.Price
	}
	sort.Float64s(sorted)

	// Compute P25 and P75 thresholds using linear interpolation
	//
	// Example with 24 values:
	//   P25 position = 24 × 0.25 = 6.0 → value at index 6 (7th value)
	//   P75 position = 24 × 0.75 = 18.0 → value at index 18 (19th value)
	//
	// Hours with price ≤ P25 are cheap → charge
	// Hours with price ≥ P75 are expensive → discharge
	p25 := percentile(sorted, 0.25)
	p75 := percentile(sorted, 0.75)

	priceByHour := make(map[int]float64, len(prices))
	for _, p := range prices {
		priceByHour[p.Hour] = p.Price
	}

	// Select hours at or below P25 for charging (the cheapest ~25% of hours)
	chargeSet := make(map[int]bool)
	for _, p := range prices {
		if p.Price <= p25 {
			chargeSet[p.Hour] = true
		}
	}

	// Select hours at or above P75 for discharging (the most expensive ~25% of hours)
	dischargeSet := make(map[int]bool)
	for _, p := range prices {
		if p.Price >= p75 {
			dischargeSet[p.Hour] = true
		}
	}

	chargeWindows := groupWindows(chargeSet, priceByHour)
	dischargeWindows := groupWindows(dischargeSet, priceByHour)

	return DaySchedule{
		ChargeWindows:    chargeWindows,
		DischargeWindows: dischargeWindows,
	}
}

// percentile computes the p-th percentile (0 ≤ p ≤ 1) from a sorted slice of values
// using linear interpolation between adjacent ranks.
//
// For n values, the position is (n × p). If position is integer, use that index directly;
// otherwise interpolate between floor and ceil indices.
func percentile(sorted []float64, p float64) float64 {
	n := float64(len(sorted))
	pos := n * p

	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))

	if lower < 0 {
		lower = 0
	}
	if upper >= len(sorted) {
		upper = len(sorted) - 1
	}
	if lower == upper {
		return sorted[lower]
	}

	// Linear interpolation between adjacent values
	frac := pos - math.Floor(pos)
	return sorted[lower] + frac*(sorted[upper]-sorted[lower])
}

// ComputeStats calculates price statistics for the day, including P25/P75 thresholds.
func ComputeStats(prices []redata.HourlyPrice) Stats {
	if len(prices) == 0 {
		return Stats{}
	}

	minP := prices[0].Price
	maxP := prices[0].Price
	sum := 0.0

	sorted := make([]float64, len(prices))
	for i, p := range prices {
		sorted[i] = p.Price
		if p.Price < minP {
			minP = p.Price
		}
		if p.Price > maxP {
			maxP = p.Price
		}
		sum += p.Price
	}
	sort.Float64s(sorted)

	return Stats{
		MinPrice: minP,
		MaxPrice: maxP,
		AvgPrice: sum / float64(len(prices)),
		P25:      percentile(sorted, 0.25),
		P75:      percentile(sorted, 0.75),
	}
}

// groupWindows merges a set of individual hours into contiguous windows.
func groupWindows(hours map[int]bool, priceByHour map[int]float64) []Window {
	if len(hours) == 0 {
		return nil
	}

	sorted := make([]int, 0, len(hours))
	for h := range hours {
		sorted = append(sorted, h)
	}
	sort.Ints(sorted)

	var windows []Window
	start := sorted[0]
	prev := sorted[0]
	priceSum := priceByHour[sorted[0]]
	count := 1

	for i := 1; i < len(sorted); i++ {
		if sorted[i] == prev+1 {
			prev = sorted[i]
			priceSum += priceByHour[sorted[i]]
			count++
		} else {
			windows = append(windows, Window{
				StartHour: start,
				EndHour:   prev + 1, // exclusive end
				AvgPrice:  priceSum / float64(count),
			})
			start = sorted[i]
			prev = sorted[i]
			priceSum = priceByHour[sorted[i]]
			count = 1
		}
	}

	windows = append(windows, Window{
		StartHour: start,
		EndHour:   prev + 1,
		AvgPrice:  priceSum / float64(count),
	})

	return windows
}
