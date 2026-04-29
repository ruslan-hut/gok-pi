package email

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"
	"time"

	"gok-pi/remote/server/sessiondb"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

var templates = func() *template.Template {
	t := template.New("").Funcs(template.FuncMap{
		"kwh":     formatKWh,
		"kw":      formatKW,
		"eur":     formatEur,
		"price":   formatPrice,
		"tlocal":  formatTimeLocal,
		"endtime": formatEndTimeLocal,
		"dlocal":  formatDateLocal,
	})
	t = template.Must(t.ParseFS(templateFS, "templates/*.gohtml"))
	return t
}()

type dailyView struct {
	Title      string
	Heading    string
	SubHeading string
	AgentID    string
	Timezone   string
	Summaries  []sessiondb.BatterySummary
	Sessions   []sessiondb.SessionRecord
	ChartSVG   template.HTML
}

type rangeTotals struct {
	ChargeEnergyWh    float64
	ChargeCostEur     float64
	DischargeEnergyWh float64
	DischargeCostEur  float64
	NetCostEur        float64
}

type rangeView struct {
	Title        string
	Heading      string
	SubHeading   string
	AgentID      string
	Timezone     string
	Start        time.Time
	EndInclusive time.Time // End - 1 day, for display only
	Summaries    []sessiondb.BatterySummary
	Totals       rangeTotals
}

// RenderDaily produces (subject, htmlBody) for a daily report.
func RenderDaily(r *DailyReport) (string, string, error) {
	loc := loadLocation(r.Timezone)
	dateStr := r.Date.In(loc).Format("Mon, 02 Jan 2006")
	devLabel := r.DeviceName
	if devLabel == "" {
		devLabel = r.AgentID
	}
	subject := fmt.Sprintf("%s — daily battery report (%s)", devLabel, dateStr)

	chart := renderPriceChartSVG(r.Prices, r.ChargeHours, r.DischargeHours, r.Stats, r.PriceLimits)

	view := dailyView{
		Title:      subject,
		Heading:    fmt.Sprintf("Daily report — %s", dateStr),
		SubHeading: fmt.Sprintf("%s (%s)", devLabel, r.AgentID),
		AgentID:    r.AgentID,
		Timezone:   r.Timezone,
		Summaries:  r.Summaries,
		Sessions:   r.Sessions,
		ChartSVG:   template.HTML(chart),
	}

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "daily.gohtml", view); err != nil {
		return "", "", fmt.Errorf("render daily: %w", err)
	}
	return subject, buf.String(), nil
}

// RenderRange produces (subject, htmlBody) for a weekly or monthly report.
func RenderRange(r *RangeReport) (string, string, error) {
	loc := loadLocation(r.Timezone)
	startStr := r.Start.In(loc).Format("02 Jan 2006")
	endInclusive := r.End.Add(-24 * time.Hour)
	endStr := endInclusive.In(loc).Format("02 Jan 2006")

	devLabel := r.DeviceName
	if devLabel == "" {
		devLabel = r.AgentID
	}

	var heading, subject string
	switch r.Period {
	case KindWeekly:
		_, isoWeek := r.Start.In(loc).ISOWeek()
		heading = fmt.Sprintf("Weekly report — Week %d", isoWeek)
		subject = fmt.Sprintf("%s — weekly battery report (%s – %s)", devLabel, startStr, endStr)
	case KindMonthly:
		monthStr := r.Start.In(loc).Format("January 2006")
		heading = fmt.Sprintf("Monthly report — %s", monthStr)
		subject = fmt.Sprintf("%s — monthly battery report (%s)", devLabel, monthStr)
	default:
		heading = "Battery report"
		subject = fmt.Sprintf("%s — battery report (%s – %s)", devLabel, startStr, endStr)
	}

	totals := computeTotals(r.Summaries)

	view := rangeView{
		Title:        subject,
		Heading:      heading,
		SubHeading:   fmt.Sprintf("%s (%s) · %s – %s", devLabel, r.AgentID, startStr, endStr),
		AgentID:      r.AgentID,
		Timezone:     r.Timezone,
		Start:        r.Start,
		EndInclusive: endInclusive,
		Summaries:    r.Summaries,
		Totals:       totals,
	}

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "range.gohtml", view); err != nil {
		return "", "", fmt.Errorf("render range: %w", err)
	}
	return subject, buf.String(), nil
}

func computeTotals(s []sessiondb.BatterySummary) rangeTotals {
	var t rangeTotals
	for _, b := range s {
		t.ChargeEnergyWh += b.ChargeEnergyWh
		t.ChargeCostEur += b.ChargeCostEur
		t.DischargeEnergyWh += b.DischargeEnergyWh
		t.DischargeCostEur += b.DischargeCostEur
		t.NetCostEur += b.NetCostEur
	}
	return t
}

// Template helper functions.

func formatKWh(wh float64) string {
	return fmt.Sprintf("%.2f", wh/1000)
}

func formatKW(w float64) string {
	return fmt.Sprintf("%.2f", w/1000)
}

func formatEur(v float64) string {
	if v == 0 {
		return "—"
	}
	sign := ""
	if v > 0 {
		sign = "+"
	}
	return fmt.Sprintf("%s%.2f €", sign, v)
}

func formatPrice(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

func formatTimeLocal(t time.Time, tz string) string {
	return t.In(loadLocation(tz)).Format("15:04")
}

func formatEndTimeLocal(t *time.Time, tz string) string {
	if t == nil {
		return "—"
	}
	return t.In(loadLocation(tz)).Format("15:04")
}

func formatDateLocal(t time.Time, tz string) string {
	return t.In(loadLocation(tz)).Format("02 Jan 2006")
}
