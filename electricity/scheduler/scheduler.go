package scheduler

import (
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
}

// ComputeSchedule analyzes hourly prices and determines optimal charge/discharge windows.
// chargeHours: how many cheapest hours to pick for charging.
// dischargeHours: how many most expensive hours to pick for discharging.
func ComputeSchedule(prices []redata.HourlyPrice, chargeHours, dischargeHours int) DaySchedule {
	if len(prices) == 0 {
		return DaySchedule{}
	}

	type ranked struct {
		hour  int
		price float64
	}

	sorted := make([]ranked, len(prices))
	for i, p := range prices {
		sorted[i] = ranked{hour: p.Hour, price: p.Price}
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].price < sorted[j].price
	})

	if chargeHours > len(sorted) {
		chargeHours = len(sorted)
	}
	if dischargeHours > len(sorted) {
		dischargeHours = len(sorted)
	}

	// Ensure charge and discharge windows don't overlap
	// by only picking from the cheapest and most expensive non-overlapping hours
	totalNeeded := chargeHours + dischargeHours
	if totalNeeded > len(sorted) {
		// Reduce equally
		chargeHours = len(sorted) / 2
		dischargeHours = len(sorted) - chargeHours
	}

	chargeSet := make(map[int]bool, chargeHours)
	for i := 0; i < chargeHours; i++ {
		chargeSet[sorted[i].hour] = true
	}

	dischargeSet := make(map[int]bool, dischargeHours)
	for i := len(sorted) - 1; i >= len(sorted)-dischargeHours; i-- {
		dischargeSet[sorted[i].hour] = true
	}

	priceByHour := make(map[int]float64, len(prices))
	for _, p := range prices {
		priceByHour[p.Hour] = p.Price
	}

	chargeWindows := groupWindows(chargeSet, priceByHour)
	dischargeWindows := groupWindows(dischargeSet, priceByHour)

	return DaySchedule{
		ChargeWindows:    chargeWindows,
		DischargeWindows: dischargeWindows,
	}
}

// ComputeStats calculates basic price statistics for the day.
func ComputeStats(prices []redata.HourlyPrice) Stats {
	if len(prices) == 0 {
		return Stats{}
	}

	min := prices[0].Price
	max := prices[0].Price
	sum := 0.0

	for _, p := range prices {
		if p.Price < min {
			min = p.Price
		}
		if p.Price > max {
			max = p.Price
		}
		sum += p.Price
	}

	return Stats{
		MinPrice: min,
		MaxPrice: max,
		AvgPrice: sum / float64(len(prices)),
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
