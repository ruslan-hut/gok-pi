package email

import (
	"fmt"
	"math"
	"strings"

	"gok-pi/electricity/redata"
	"gok-pi/electricity/scheduler"
)

// renderPriceChartHTML returns a self-contained HTML fragment showing 24 hourly
// prices as colored bars, plus a stats strip (P20/avg/P80, optional user limits).
//
// Implementation notes — why HTML tables instead of SVG:
//
// Inline SVG is stripped by Gmail and many other webmail clients (only its inner
// text content survives). A `<table>` with each cell as a fixed-height bar is the
// most universally supported chart primitive in HTML email — it renders in Gmail
// (web/mobile), Apple Mail, Outlook, Thunderbird, and corporate webmail.
//
// Threshold lines (P20/P80/Avg) are presented as a labeled strip rather than
// drawn over the bars: absolute positioning is unreliable across email clients.
func renderPriceChartHTML(
	prices []redata.HourlyPrice,
	chargeHours, dischargeHours map[int]bool,
	stats scheduler.Stats,
	limits PriceLimits,
) string {
	if len(prices) == 0 {
		return ""
	}

	const (
		chartHeight = 160 // px

		colorBar     = "#9ca3af"
		colorSuccess = "#16a34a"
		colorDanger  = "#dc2626"
		colorMuted   = "#6b7280"
		colorLine    = "#e2e8f0"
	)

	maxPrice := math.Max(stats.MaxPrice*1.1, 1)
	minPrice := math.Min(0, stats.MinPrice)
	span := maxPrice - minPrice
	if span == 0 {
		span = 1
	}

	// Map price → bar height in px. Negative prices clamp to 0 height (rare on
	// PVPC and noting them in the stats strip is enough for an email summary).
	barHeight := func(p float64) int {
		if p <= 0 {
			return 0
		}
		h := int(math.Round((p / maxPrice) * float64(chartHeight)))
		if h < 1 {
			h = 1
		}
		if h > chartHeight {
			h = chartHeight
		}
		return h
	}

	priceByHour := make(map[int]float64, len(prices))
	for _, p := range prices {
		priceByHour[p.Hour] = p.Price
	}

	var b strings.Builder

	// Outer frame
	b.WriteString(`<div style="border:1px solid ` + colorLine + `;border-radius:6px;padding:10px;background:#ffffff;">`)

	// Bars row
	b.WriteString(`<table role="presentation" cellspacing="0" cellpadding="0" border="0" style="width:100%;border-collapse:collapse;table-layout:fixed;font-family:-apple-system,Segoe UI,Roboto,sans-serif;">`)
	b.WriteString(`<tr>`)
	for h := 0; h < 24; h++ {
		price := priceByHour[h]
		bh := barHeight(price)

		fill := colorBar
		if chargeHours[h] {
			fill = colorSuccess
		}
		if dischargeHours[h] {
			fill = colorDanger
		}

		// Each cell is the full chart height; bar div sits at the bottom.
		fmt.Fprintf(&b,
			`<td valign="bottom" style="height:%dpx;padding:0 1px;vertical-align:bottom;line-height:0;">`+
				`<div title="%02d:00 — %.1f €/MWh" style="background:%s;height:%dpx;border-top-left-radius:2px;border-top-right-radius:2px;line-height:0;font-size:0;">&nbsp;</div>`+
				`</td>`,
			chartHeight, h, price, fill, bh,
		)
	}
	b.WriteString(`</tr>`)

	// Hour ticks (every 3 hours)
	b.WriteString(`<tr>`)
	for h := 0; h < 24; h++ {
		label := ""
		if h%3 == 0 {
			label = fmt.Sprintf("%02d", h)
		}
		fmt.Fprintf(&b,
			`<td style="padding:4px 0 0 0;text-align:center;font-size:10px;color:%s;line-height:1;">%s</td>`,
			colorMuted, label,
		)
	}
	b.WriteString(`</tr>`)
	b.WriteString(`</table>`)

	// Stats strip
	b.WriteString(`<div style="margin-top:8px;font-size:11px;color:#475569;font-family:-apple-system,Segoe UI,Roboto,sans-serif;line-height:1.5;">`)
	b.WriteString(legendChip(colorSuccess, fmt.Sprintf("≤ P%d (€%.0f)", stats.ChargePercentile, stats.Low)))
	b.WriteString(`&nbsp;&middot;&nbsp;`)
	b.WriteString(legendChip(colorDanger, fmt.Sprintf("≥ P%d (€%.0f)", stats.DischargePercentile, stats.High)))
	b.WriteString(`&nbsp;&middot;&nbsp;`)
	b.WriteString(fmt.Sprintf(`<span>Avg €%.1f</span>`, stats.AvgPrice))
	b.WriteString(`&nbsp;&middot;&nbsp;`)
	b.WriteString(fmt.Sprintf(`<span>Min €%.1f</span>`, stats.MinPrice))
	b.WriteString(`&nbsp;&middot;&nbsp;`)
	b.WriteString(fmt.Sprintf(`<span>Max €%.1f</span>`, stats.MaxPrice))

	if limits.ChargeLimitEurMWh > 0 || limits.DischargeLimitEurMWh > 0 {
		b.WriteString(`<br/>`)
		b.WriteString(`<span style="color:#64748b;">Limits:</span>&nbsp;`)
		if limits.ChargeLimitEurMWh > 0 {
			b.WriteString(fmt.Sprintf(`<span style="color:%s;">charge ≤ €%.0f</span>`, colorSuccess, limits.ChargeLimitEurMWh))
		}
		if limits.ChargeLimitEurMWh > 0 && limits.DischargeLimitEurMWh > 0 {
			b.WriteString(`&nbsp;&middot;&nbsp;`)
		}
		if limits.DischargeLimitEurMWh > 0 {
			b.WriteString(fmt.Sprintf(`<span style="color:%s;">discharge ≥ €%.0f</span>`, colorDanger, limits.DischargeLimitEurMWh))
		}
	}

	b.WriteString(`</div>`)

	b.WriteString(`</div>`)
	return b.String()
}

func legendChip(color, label string) string {
	return fmt.Sprintf(
		`<span style="display:inline-block;width:8px;height:8px;background:%s;border-radius:2px;vertical-align:middle;margin-right:4px;"></span><span style="vertical-align:middle;">%s</span>`,
		color, label,
	)
}
