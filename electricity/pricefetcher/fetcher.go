package pricefetcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gok-pi/electricity/redata"
	"gok-pi/electricity/scheduler"
)

// DayData holds prices and computed schedule for a single day.
type DayData struct {
	Date     string                `json:"date"`
	Prices   []redata.HourlyPrice  `json:"prices"`
	Schedule scheduler.DaySchedule `json:"schedule"`
	Stats    scheduler.Stats       `json:"stats"`
}

// State represents the current state of the price fetcher, served to the UI.
type State struct {
	Today       *DayData  `json:"today"`
	Tomorrow    *DayData  `json:"tomorrow"`
	LastUpdated time.Time `json:"last_updated"`
	LastError   string    `json:"last_error,omitempty"`
	NextUpdate  time.Time `json:"next_update"`
}

type Fetcher struct {
	client         *redata.Client
	log            *slog.Logger
	chargeHours    int
	dischargeHours int

	mu    sync.RWMutex
	state State
}

func New(log *slog.Logger, chargeHours, dischargeHours int) *Fetcher {
	if chargeHours <= 0 {
		chargeHours = 5
	}
	if dischargeHours <= 0 {
		dischargeHours = 5
	}

	return &Fetcher{
		client:         redata.NewClient(log),
		log:            log.With(slog.String("component", "price-fetcher")),
		chargeHours:    chargeHours,
		dischargeHours: dischargeHours,
	}
}

// GetState returns a snapshot of the current price data.
func (f *Fetcher) GetState() State {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.state
}

// Run starts the background fetch loop. Blocks until ctx is cancelled.
func (f *Fetcher) Run(ctx context.Context) {
	f.log.Info("price fetcher started", slog.Int("charge_hours", f.chargeHours), slog.Int("discharge_hours", f.dischargeHours))

	// Initial fetch
	f.fetchAll(ctx)

	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			f.log.Info("price fetcher stopped")
			return
		case <-ticker.C:
			f.fetchAll(ctx)
		}
	}
}

func (f *Fetcher) fetchAll(ctx context.Context) {
	now := f.madridNow()
	today := now.Truncate(24 * time.Hour)
	tomorrow := today.Add(24 * time.Hour)

	var lastErr string

	todayData, err := f.fetchDay(ctx, today)
	if err != nil {
		lastErr = fmt.Sprintf("today: %s", err)
		f.log.Warn("failed to fetch today's prices", slog.Any("error", err))
	}

	var tomorrowData *DayData
	// Only try to fetch tomorrow after 20:00 CET
	if now.Hour() >= 20 {
		td, err := f.fetchDay(ctx, tomorrow)
		if err != nil {
			if !errors.Is(err, redata.ErrNoData) {
				errMsg := fmt.Sprintf("tomorrow: %s", err)
				if lastErr != "" {
					lastErr = lastErr + "; " + errMsg
				} else {
					lastErr = errMsg
				}
				f.log.Warn("failed to fetch tomorrow's prices", slog.Any("error", err))
			} else {
				f.log.Debug("tomorrow's prices not yet available")
			}
		} else {
			tomorrowData = td
		}
	}

	nextUpdate := f.computeNextUpdate(now, tomorrowData != nil)

	f.mu.Lock()
	// Keep existing data if new fetch failed
	if todayData != nil {
		f.state.Today = todayData
	}
	if tomorrowData != nil {
		f.state.Tomorrow = tomorrowData
	}
	// Clear stale tomorrow data if it's now today
	if f.state.Tomorrow != nil && f.state.Tomorrow.Date == today.Format("2006-01-02") {
		f.state.Tomorrow = nil
	}
	f.state.LastUpdated = time.Now().UTC()
	f.state.LastError = lastErr
	f.state.NextUpdate = nextUpdate
	f.mu.Unlock()
}

func (f *Fetcher) fetchDay(ctx context.Context, date time.Time) (*DayData, error) {
	prices, err := f.client.FetchPrices(ctx, date)
	if err != nil {
		return nil, err
	}

	sched := scheduler.ComputeSchedule(prices, f.chargeHours, f.dischargeHours)
	stats := scheduler.ComputeStats(prices)

	return &DayData{
		Date:     date.Format("2006-01-02"),
		Prices:   prices,
		Schedule: sched,
		Stats:    stats,
	}, nil
}

func (f *Fetcher) computeNextUpdate(now time.Time, haveTomorrow bool) time.Time {
	if now.Hour() >= 20 && !haveTomorrow {
		// Retry more frequently when waiting for tomorrow's prices
		return now.Add(15 * time.Minute)
	}
	if now.Hour() < 20 {
		// Next meaningful update is at 20:00 when tomorrow's prices appear
		next := time.Date(now.Year(), now.Month(), now.Day(), 20, 15, 0, 0, now.Location())
		return next
	}
	// We have everything, next update in 1 hour
	return now.Add(1 * time.Hour)
}

func (f *Fetcher) madridNow() time.Time {
	loc, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		f.log.Warn("failed to load Europe/Madrid timezone, using UTC+1", slog.Any("error", err))
		loc = time.FixedZone("CET", 3600)
	}
	return time.Now().In(loc)
}
