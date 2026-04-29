package email

import (
	"strings"
	"testing"
	"time"

	"gok-pi/electricity/redata"
	"gok-pi/electricity/scheduler"
	"gok-pi/remote/server/sessiondb"
)

func TestRenderDailyContainsCoreFields(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Madrid")
	day := time.Date(2026, 4, 28, 0, 0, 0, 0, loc)
	end := day.Add(9 * time.Hour)

	rep := &DailyReport{
		AgentID:    "agent-xyz",
		DeviceName: "MyHome",
		Date:       day,
		Timezone:   "Europe/Madrid",
		Summaries: []sessiondb.BatterySummary{
			{BatteryName: "bat-A", ChargeEnergyWh: 2000, ChargeCostEur: -0.40, DischargeEnergyWh: 1500, DischargeCostEur: 0.55, NetCostEur: 0.15},
		},
		Sessions: []sessiondb.SessionRecord{
			{
				BatteryName: "bat-A", Type: "discharge",
				StartedAt: day.Add(8 * time.Hour), EndedAt: &end,
				EnergyWh: 1500, AvgPowerW: 1500, AvgPriceEurMWh: 200, CostEur: 0.55,
			},
		},
		Prices: makePrices(),
		Stats:  makeStats(),
	}
	rep.ChargeHours = map[int]bool{2: true, 3: true}
	rep.DischargeHours = map[int]bool{19: true, 20: true}
	rep.PriceLimits = PriceLimits{ChargeLimitEurMWh: 50, DischargeLimitEurMWh: 250}

	subject, html, err := RenderDaily(rep)
	if err != nil {
		t.Fatalf("RenderDaily: %v", err)
	}
	if !strings.Contains(subject, "MyHome") || !strings.Contains(subject, "28") {
		t.Fatalf("subject missing fields: %q", subject)
	}
	for _, want := range []string{"agent-xyz", "bat-A", "Daily report", "<table", "Hourly prices", "1.50", "Avg €", "P20"} {
		if !strings.Contains(html, want) {
			t.Fatalf("html missing %q\n--- html ---\n%s", want, truncForTest(html, 600))
		}
	}
}

func TestRenderRangeWeekly(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 7)

	rep := &RangeReport{
		AgentID:    "agent-xyz",
		DeviceName: "MyHome",
		Period:     KindWeekly,
		Start:      start,
		End:        end,
		Timezone:   "UTC",
		Summaries: []sessiondb.BatterySummary{
			{BatteryName: "bat-A", ChargeEnergyWh: 8000, ChargeCostEur: -1.20, DischargeEnergyWh: 6000, DischargeCostEur: 1.80, NetCostEur: 0.60},
		},
	}
	subject, html, err := RenderRange(rep)
	if err != nil {
		t.Fatalf("RenderRange: %v", err)
	}
	if !strings.Contains(subject, "weekly") {
		t.Fatalf("weekly subject: %q", subject)
	}
	for _, want := range []string{"Week", "bat-A", "Total", "8.00", "20 Apr 2026", "26 Apr 2026"} {
		if !strings.Contains(html, want) {
			t.Fatalf("weekly html missing %q\n%s", want, truncForTest(html, 600))
		}
	}
}

func TestRenderChartHTMLBasics(t *testing.T) {
	html := renderPriceChartHTML(makePrices(), map[int]bool{2: true}, map[int]bool{19: true}, makeStats(), PriceLimits{ChargeLimitEurMWh: 50, DischargeLimitEurMWh: 200})
	if !strings.Contains(html, "<table") || !strings.Contains(html, "</table>") {
		t.Fatalf("table envelope missing: %q", truncForTest(html, 200))
	}
	// 24 bar cells expected (one <td valign="bottom"> per hour)
	if c := strings.Count(html, `valign="bottom"`); c != 24 {
		t.Fatalf("expected 24 bar cells, got %d", c)
	}
	// Charge/discharge colors and limits should appear
	for _, want := range []string{"#16a34a", "#dc2626", "Limits:", "charge ≤", "discharge ≥"} {
		if !strings.Contains(html, want) {
			t.Fatalf("html missing %q\n--- html ---\n%s", want, truncForTest(html, 800))
		}
	}
}

func makePrices() []redata.HourlyPrice {
	out := make([]redata.HourlyPrice, 24)
	for i := 0; i < 24; i++ {
		// Synthetic curve: cheap at night, expensive in evening
		var p float64
		switch {
		case i < 6:
			p = 30 + float64(i)
		case i < 18:
			p = 80 + float64(i-6)*5
		default:
			p = 220 - float64(i-18)*10
		}
		out[i] = redata.HourlyPrice{Hour: i, Price: p, DateTime: time.Date(2026, 4, 28, i, 0, 0, 0, time.UTC)}
	}
	return out
}

func makeStats() scheduler.Stats {
	return scheduler.Stats{
		MinPrice:            30,
		MaxPrice:            220,
		AvgPrice:            120,
		Low:                 35,
		High:                200,
		ChargePercentile:    20,
		DischargePercentile: 80,
	}
}

func truncForTest(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
