package email

import (
	"fmt"
	"math"
	"strings"

	"gok-pi/electricity/redata"
	"gok-pi/electricity/scheduler"
)

// renderPriceChartSVG returns a self-contained SVG string visualising 24 hourly
// prices, charge/discharge windows, P20/P80 thresholds, the daily average and
// optional user-set price limits. It is a Go port of the React PriceChart in
// web/ui/src/components/prices/PricesDashboard.tsx — same dimensions, same
// visual rules, no external deps. All styling is inlined so the output renders
// in the most common HTML email clients (Apple Mail, Thunderbird, Gmail web).
func renderPriceChartSVG(
	prices []redata.HourlyPrice,
	chargeHours, dischargeHours map[int]bool,
	stats scheduler.Stats,
	limits PriceLimits,
) string {
	if len(prices) == 0 {
		return ""
	}

	const (
		width     = 720
		height    = 200
		padTop    = 20.0
		padRight  = 12.0
		padBottom = 28.0
		padLeft   = 48.0

		colorMutedLine = "#dfe3e8"
		colorMutedText = "#6b7280"
		colorBar       = "#9ca3af"
		colorSuccess   = "#16a34a"
		colorDanger    = "#dc2626"
		colorWarning   = "#d97706"
		bgFill         = "#ffffff"
	)

	chartW := float64(width) - padLeft - padRight
	chartH := float64(height) - padTop - padBottom

	maxPrice := math.Max(stats.MaxPrice*1.1, 1)
	minPrice := math.Min(0, stats.MinPrice)
	priceRange := maxPrice - minPrice
	if priceRange == 0 {
		priceRange = 1
	}
	barW := chartW/24 - 2

	yScale := func(v float64) float64 {
		return padTop + chartH - ((v-minPrice)/priceRange)*chartH
	}
	zeroY := yScale(0)

	step := niceStep(priceRange, 5)
	var gridLines []float64
	for v := math.Ceil(minPrice/step) * step; v <= maxPrice; v += step {
		gridLines = append(gridLines, v)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" role="img" aria-label="Hourly electricity prices" style="background:%s;font-family:-apple-system,BlinkMacSystemFont,Segoe UI,Roboto,sans-serif">`,
		width, height, width, height, bgFill)

	// Grid
	for _, v := range gridLines {
		y := yScale(v)
		fmt.Fprintf(&b,
			`<line x1="%.1f" x2="%.1f" y1="%.2f" y2="%.2f" stroke="%s" stroke-dasharray="2,3"/>`,
			padLeft, float64(width)-padRight, y, y, colorMutedLine,
		)
		fmt.Fprintf(&b,
			`<text x="%.1f" y="%.2f" text-anchor="end" fill="%s" font-size="10">%.0f</text>`,
			padLeft-6, y+3, colorMutedText, v,
		)
	}

	// Threshold lines: low (P20)
	thresholdLine(&b, padLeft, float64(width)-padRight, yScale(stats.Low), colorSuccess, "4,3", 1, 0.5)
	// High (P80)
	thresholdLine(&b, padLeft, float64(width)-padRight, yScale(stats.High), colorDanger, "4,3", 1, 0.5)
	// Average
	thresholdLine(&b, padLeft, float64(width)-padRight, yScale(stats.AvgPrice), colorWarning, "4,3", 1, 0.6)

	// Bars
	for _, p := range prices {
		x := padLeft + (float64(p.Hour)/24)*chartW + 1
		barTop := yScale(math.Max(p.Price, 0))
		var barBottom float64
		if p.Price >= 0 {
			barBottom = zeroY
		} else {
			barBottom = yScale(p.Price)
		}
		barHeight := math.Abs(barBottom - barTop)
		if barHeight < 1 {
			barHeight = 1
		}
		y := math.Min(barTop, barBottom)

		fill := colorBar
		if chargeHours[p.Hour] {
			fill = colorSuccess
		}
		if dischargeHours[p.Hour] {
			fill = colorDanger
		}
		fmt.Fprintf(&b,
			`<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="2" fill="%s"/>`,
			x, y, barW, barHeight, fill,
		)
	}

	// User limit lines (drawn over bars)
	if limits.ChargeLimitEurMWh > 0 {
		thresholdLine(&b, padLeft, float64(width)-padRight, yScale(limits.ChargeLimitEurMWh), colorSuccess, "6,3", 1.5, 0.9)
	}
	if limits.DischargeLimitEurMWh > 0 {
		thresholdLine(&b, padLeft, float64(width)-padRight, yScale(limits.DischargeLimitEurMWh), colorDanger, "6,3", 1.5, 0.9)
	}

	// X-axis hour labels
	for _, h := range []int{0, 3, 6, 9, 12, 15, 18, 21} {
		x := padLeft + (float64(h)/24)*chartW + barW/2
		fmt.Fprintf(&b,
			`<text x="%.2f" y="%.2f" text-anchor="middle" fill="%s" font-size="10">%02d</text>`,
			x, float64(height)-6, colorMutedText, h,
		)
	}

	// Legend
	legendX := float64(width) - 170
	fmt.Fprintf(&b, `<rect x="%.0f" y="4" width="10" height="10" rx="2" fill="%s"/>`, legendX, colorSuccess)
	fmt.Fprintf(&b, `<text x="%.0f" y="13" fill="%s" font-size="10">&#8804; P%d</text>`, legendX+14, colorMutedText, stats.ChargePercentile)
	fmt.Fprintf(&b, `<rect x="%.0f" y="4" width="10" height="10" rx="2" fill="%s"/>`, legendX+50, colorDanger)
	fmt.Fprintf(&b, `<text x="%.0f" y="13" fill="%s" font-size="10">&#8805; P%d</text>`, legendX+64, colorMutedText, stats.DischargePercentile)
	fmt.Fprintf(&b,
		`<line x1="%.0f" x2="%.0f" y1="9" y2="9" stroke="%s" stroke-dasharray="4,3" opacity="0.6"/>`,
		legendX+124, legendX+138, colorWarning,
	)
	fmt.Fprintf(&b, `<text x="%.0f" y="13" fill="%s" font-size="10">Avg</text>`, legendX+142, colorMutedText)

	b.WriteString(`</svg>`)
	return b.String()
}

func thresholdLine(b *strings.Builder, x1, x2, y float64, color, dash string, width, opacity float64) {
	fmt.Fprintf(b,
		`<line x1="%.1f" x2="%.1f" y1="%.2f" y2="%.2f" stroke="%s" stroke-dasharray="%s" stroke-width="%.1f" opacity="%.2f"/>`,
		x1, x2, y, y, color, dash, width, opacity,
	)
}

// niceStep mirrors the niceStep helper in PricesDashboard.tsx.
func niceStep(priceRange float64, targetTicks int) float64 {
	if priceRange <= 0 || targetTicks <= 0 {
		return 1
	}
	rough := priceRange / float64(targetTicks)
	mag := math.Pow(10, math.Floor(math.Log10(rough)))
	norm := rough / mag
	var s float64
	switch {
	case norm <= 1.5:
		s = 1
	case norm <= 3:
		s = 2
	case norm <= 7:
		s = 5
	default:
		s = 10
	}
	return s * mag
}
