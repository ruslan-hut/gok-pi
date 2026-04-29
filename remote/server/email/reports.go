package email

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"gok-pi/electricity/pricefetcher"
	"gok-pi/electricity/redata"
	"gok-pi/electricity/scheduler"
	"gok-pi/remote/server/sessiondb"
)

// PriceLimits mirrors the server-side PriceLimits type. The email package keeps its own
// definition to avoid an import cycle with remote/server.
type PriceLimits struct {
	ChargeLimitEurMWh    float64
	DischargeLimitEurMWh float64
}

// ReportKind identifies which report period is being generated.
type ReportKind string

const (
	KindDaily   ReportKind = "daily"
	KindWeekly  ReportKind = "weekly"
	KindMonthly ReportKind = "monthly"
)

// DailyReport is the data passed to the daily HTML template.
type DailyReport struct {
	AgentID    string
	DeviceName string
	Date       time.Time // local date (00:00 in agent tz)
	Timezone   string

	Sessions  []sessiondb.SessionRecord
	Summaries []sessiondb.BatterySummary

	Prices         []redata.HourlyPrice
	Stats          scheduler.Stats
	ChargeHours    map[int]bool
	DischargeHours map[int]bool
	PriceLimits    PriceLimits
}

// RangeReport is shared by weekly and monthly reports.
type RangeReport struct {
	AgentID    string
	DeviceName string
	Period     ReportKind // KindWeekly or KindMonthly
	Start      time.Time  // inclusive (local)
	End        time.Time  // exclusive (local)
	Timezone   string

	Sessions  []sessiondb.SessionRecord  // populated for weekly only
	Summaries []sessiondb.BatterySummary // always populated
}

// ReportBuilder constructs reports from the session DB and price fetcher.
type ReportBuilder struct {
	store  *sessiondb.Store
	prices *pricefetcher.Fetcher
}

// NewReportBuilder constructs a builder. prices may be nil; the daily report will then
// attempt the REData API directly via prices.Client(). If both are unavailable the chart is omitted.
func NewReportBuilder(store *sessiondb.Store, prices *pricefetcher.Fetcher) *ReportBuilder {
	return &ReportBuilder{store: store, prices: prices}
}

// BuildDaily produces a daily report covering the local day starting at `day`.
// `day` must already be normalised to 00:00 in the agent's timezone.
func (b *ReportBuilder) BuildDaily(ctx context.Context, agentID, deviceName, tz string, day time.Time, limits PriceLimits) (*DailyReport, error) {
	if b.store == nil {
		return nil, errors.New("session store unavailable")
	}

	start := day
	end := day.Add(24 * time.Hour)

	sessions, err := b.querySessions(agentID, start, end)
	if err != nil {
		return nil, fmt.Errorf("query daily sessions: %w", err)
	}
	summaries, err := b.store.GetSummariesRange(agentID, start.UTC(), end.UTC())
	if err != nil {
		return nil, fmt.Errorf("query daily summaries: %w", err)
	}
	sortSummaries(summaries)

	r := &DailyReport{
		AgentID:     agentID,
		DeviceName:  deviceName,
		Date:        day,
		Timezone:    tz,
		Sessions:    sessions,
		Summaries:   summaries,
		PriceLimits: limits,
	}

	prices := b.fetchDayPrices(ctx, day)
	if len(prices) > 0 {
		r.Prices = prices
		r.Stats = scheduler.ComputeStats(prices)
		sched := scheduler.ComputeSchedule(prices)
		r.ChargeHours = hoursFromWindows(sched.ChargeWindows)
		r.DischargeHours = hoursFromWindows(sched.DischargeWindows)
	}

	return r, nil
}

// BuildRange produces a weekly or monthly aggregate report for [start, end).
// Bounds are interpreted in the agent timezone but converted to UTC for queries.
func (b *ReportBuilder) BuildRange(agentID, deviceName, tz string, kind ReportKind, start, end time.Time) (*RangeReport, error) {
	if b.store == nil {
		return nil, errors.New("session store unavailable")
	}

	summaries, err := b.store.GetSummariesRange(agentID, start.UTC(), end.UTC())
	if err != nil {
		return nil, fmt.Errorf("query range summaries: %w", err)
	}
	sortSummaries(summaries)

	r := &RangeReport{
		AgentID:    agentID,
		DeviceName: deviceName,
		Period:     kind,
		Start:      start,
		End:        end,
		Timezone:   tz,
		Summaries:  summaries,
	}

	if kind == KindWeekly {
		sessions, err := b.querySessions(agentID, start, end)
		if err != nil {
			return nil, fmt.Errorf("query weekly sessions: %w", err)
		}
		r.Sessions = sessions
	}

	return r, nil
}

func (b *ReportBuilder) querySessions(agentID string, start, end time.Time) ([]sessiondb.SessionRecord, error) {
	res, err := b.store.QuerySessions(sessiondb.SessionQuery{
		AgentID:  agentID,
		DateFrom: start.UTC().Format(time.RFC3339),
		DateTo:   end.UTC().Format(time.RFC3339),
		Status:   "closed",
		Limit:    500,
	})
	if err != nil {
		return nil, err
	}
	// Order chronologically for the email (QuerySessions returns newest-first).
	sort.Slice(res.Records, func(i, j int) bool {
		return res.Records[i].StartedAt.Before(res.Records[j].StartedAt)
	})
	return res.Records, nil
}

// fetchDayPrices returns the 24 hourly prices for the requested calendar day, or nil
// if neither cache nor REData yields data. The day is interpreted by its date portion.
func (b *ReportBuilder) fetchDayPrices(ctx context.Context, day time.Time) []redata.HourlyPrice {
	dayStr := day.Format("2006-01-02")

	if b.prices != nil {
		state := b.prices.GetState()
		if state.Today != nil && state.Today.Date == dayStr {
			return state.Today.Prices
		}
		if state.Tomorrow != nil && state.Tomorrow.Date == dayStr {
			return state.Tomorrow.Prices
		}
		if client := b.prices.Client(); client != nil {
			fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			prices, err := client.FetchPrices(fetchCtx, day)
			if err == nil {
				return prices
			}
		}
	}
	return nil
}

func hoursFromWindows(windows []scheduler.Window) map[int]bool {
	out := make(map[int]bool)
	for _, w := range windows {
		for h := w.StartHour; h < w.EndHour; h++ {
			out[h] = true
		}
	}
	return out
}

func sortSummaries(s []sessiondb.BatterySummary) {
	sort.Slice(s, func(i, j int) bool { return s[i].BatteryName < s[j].BatteryName })
}

// LocalDay returns the start (00:00) of the given calendar day in the supplied timezone.
// If tz is empty or invalid, UTC is used.
func LocalDay(tz string, t time.Time) time.Time {
	loc := loadLocation(tz)
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// LocalNow returns time.Now() in the supplied timezone (UTC fallback).
func LocalNow(tz string) time.Time {
	return time.Now().In(loadLocation(tz))
}

// MondayOfWeek returns the Monday 00:00 of the ISO week containing t (in t's location).
func MondayOfWeek(t time.Time) time.Time {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7 // Sunday → 7
	}
	monday := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).
		AddDate(0, 0, -(wd - 1))
	return monday
}

// FirstOfMonth returns the first day 00:00 of the month containing t (in t's location).
func FirstOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

func loadLocation(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}
