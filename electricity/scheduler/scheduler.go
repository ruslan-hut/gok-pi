// Package scheduler analyzes hourly electricity prices to determine optimal
// charge and discharge time windows for battery systems.
//
// Algorithm — P20/P80 Percentile Strategy:
//
//  1. Sort all 24 hourly prices ascending
//  2. Compute P20 (20th percentile) — the price threshold below which to charge (buy)
//  3. Compute P80 (80th percentile) — the price threshold above which to discharge (sell)
//  4. Select all hours with price ≤ P20 as charge hours
//  5. Select all hours with price ≥ P80 as discharge hours
//  6. Merge adjacent hours into contiguous windows (e.g., hours 2,3,4 → window 02:00-05:00)
//
// This strategy is self-adaptive: thresholds recalculate daily based on actual
// price distribution from REE forecast. On flat-price days fewer hours qualify
// (avoiding unprofitable cycling), on volatile days more hours qualify at extremes
// (capturing more opportunity). The logic buys at the lower percentile and sells at
// the upper percentile, guaranteeing operations always occur at the extremes of the
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

const (
	// ChargePercentile is the percentile threshold for charging — hours with price ≤ this are cheap.
	ChargePercentile = 0.20
	// DischargePercentile is the percentile threshold for discharging — hours with price ≥ this are expensive.
	DischargePercentile = 0.80
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
	MinPrice            float64 `json:"min_price_eur_mwh"`
	MaxPrice            float64 `json:"max_price_eur_mwh"`
	AvgPrice            float64 `json:"avg_price_eur_mwh"`
	Low                 float64 `json:"low_eur_mwh"`          // low percentile threshold — charge below this
	High                float64 `json:"high_eur_mwh"`         // high percentile threshold — discharge above this
	ChargePercentile    int     `json:"charge_percentile"`    // e.g. 20 (means P20)
	DischargePercentile int     `json:"discharge_percentile"` // e.g. 80 (means P80)
}

// ComputeSchedule analyzes hourly prices using a P20/P80 percentile strategy.
//
// How it works:
//   - P20 (20th percentile): 20% of hours have a price ≤ this value — these are
//     the cheap hours suitable for charging.
//   - P80 (80th percentile): only 20% of hours have a price ≥ this value — these
//     are the expensive hours suitable for discharging/selling.
//   - Hours with price ≤ P20 → charge windows (buy at the lower percentile)
//   - Hours with price ≥ P80 → discharge windows (sell at the upper percentile)
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

	// Compute P20 and P80 thresholds using linear interpolation
	//
	// Example with 24 values:
	//   P20 position = 24 × 0.20 = 4.8 → interpolated between index 4 and 5
	//   P80 position = 24 × 0.80 = 19.2 → interpolated between index 19 and 20
	//
	// Hours with price ≤ P20 are cheap → charge
	// Hours with price ≥ P80 are expensive → discharge
	low := percentile(sorted, ChargePercentile)
	high := percentile(sorted, DischargePercentile)

	priceByHour := make(map[int]float64, len(prices))
	for _, p := range prices {
		priceByHour[p.Hour] = p.Price
	}

	// Select hours at or below P20 for charging (the cheapest ~20% of hours)
	chargeSet := make(map[int]bool)
	for _, p := range prices {
		if p.Price <= low {
			chargeSet[p.Hour] = true
		}
	}

	// Select hours at or above P80 for discharging (the most expensive ~20% of hours)
	dischargeSet := make(map[int]bool)
	for _, p := range prices {
		if p.Price >= high {
			dischargeSet[p.Hour] = true
		}
	}

	// On flat/degenerate days the P20 and P80 thresholds can coincide (or nearly),
	// so an hour may satisfy both price ≤ low and price ≥ high. Emitting both would
	// produce a conflicting charge and discharge window for the same hour, so drop
	// any overlap from both sets rather than act on an ambiguous signal.
	for h := range chargeSet {
		if dischargeSet[h] {
			delete(chargeSet, h)
			delete(dischargeSet, h)
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

// ComputeStats calculates price statistics for the day, including P20/P80 thresholds.
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
		MinPrice:            minP,
		MaxPrice:            maxP,
		AvgPrice:            sum / float64(len(prices)),
		Low:                 percentile(sorted, ChargePercentile),
		High:                percentile(sorted, DischargePercentile),
		ChargePercentile:    int(ChargePercentile * 100),
		DischargePercentile: int(DischargePercentile * 100),
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
