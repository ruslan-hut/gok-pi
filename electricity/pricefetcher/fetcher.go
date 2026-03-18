// Package pricefetcher provides a background poller that fetches hourly electricity
// prices from the Spanish PVPC market (REData API) and computes optimal charge/discharge
// schedules using the scheduler package.
//
// It fetches today's and tomorrow's prices (tomorrow available after ~20:30 CET),
// and exposes the current state via GetState() for the control server's auto-scheduler
// and REST API (/api/prices).
package pricefetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

// Previously used fixed Top-N constants (chargePeriods=3, dischargePeriods=3).
// Now replaced by P20/P80 percentile strategy in the scheduler package —
// the number of charge/discharge hours adapts automatically to the daily price distribution.

type Fetcher struct {
	client    *redata.Client
	log       *slog.Logger
	cachePath string

	mu    sync.RWMutex
	state State
}

func New(log *slog.Logger, cachePath string) *Fetcher {
	f := &Fetcher{
		client:    redata.NewClient(log),
		log:       log.With(slog.String("component", "price-fetcher")),
		cachePath: cachePath,
	}
	f.loadCache()
	return f
}

// GetState returns a snapshot of the current price data.
func (f *Fetcher) GetState() State {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.state
}

// Run starts the background fetch loop. Blocks until ctx is cancelled.
// The loop uses adaptive scheduling: it sleeps until the next meaningful
// update time computed by computeNextUpdate, avoiding redundant API calls
// when data is already loaded.
func (f *Fetcher) Run(ctx context.Context) {
	f.log.Info("price fetcher started (P20/P80 percentile strategy)")

	for {
		f.fetchAll(ctx)

		f.mu.RLock()
		nextUpdate := f.state.NextUpdate
		f.mu.RUnlock()

		delay := time.Until(nextUpdate)
		if delay < time.Minute {
			delay = time.Minute
		}
		f.log.Debug("next price fetch scheduled", slog.Time("at", nextUpdate), slog.Duration("in", delay))

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.log.Info("price fetcher stopped")
			return
		case <-timer.C:
		}
	}
}

func (f *Fetcher) fetchAll(ctx context.Context) {
	now := f.madridNow()
	todayStr := now.Format("2006-01-02")
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tomorrow := today.Add(24 * time.Hour)

	var lastErr string

	// Skip fetching today's prices if we already have them for the current date.
	// Prices don't change within a day, so re-fetching is redundant.
	f.mu.RLock()
	haveToday := f.state.Today != nil && f.state.Today.Date == todayStr
	haveTomorrow := f.state.Tomorrow != nil && f.state.Tomorrow.Date == tomorrow.Format("2006-01-02")
	f.mu.RUnlock()

	var todayData *DayData
	if !haveToday {
		var err error
		todayData, err = f.fetchDay(ctx, today)
		if err != nil {
			lastErr = fmt.Sprintf("today: %s", err)
			f.log.Warn("failed to fetch today's prices", slog.Any("error", err))
		}
	}

	var tomorrowData *DayData
	// Only try to fetch tomorrow after 20:00 CET, and only if we don't have it yet
	if now.Hour() >= 20 && !haveTomorrow {
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

	nextUpdate := f.computeNextUpdate(now, haveTomorrow || tomorrowData != nil)

	f.mu.Lock()
	if todayData != nil {
		f.state.Today = todayData
	}
	if tomorrowData != nil {
		f.state.Tomorrow = tomorrowData
	}
	// Clear stale tomorrow data if it's now today
	if f.state.Tomorrow != nil && f.state.Tomorrow.Date == todayStr {
		f.state.Tomorrow = nil
	}
	f.state.LastUpdated = time.Now().UTC()
	f.state.LastError = lastErr
	f.state.NextUpdate = nextUpdate
	f.mu.Unlock()

	if todayData != nil || tomorrowData != nil {
		f.saveCache()
	}
}

func (f *Fetcher) fetchDay(ctx context.Context, date time.Time) (*DayData, error) {
	prices, err := f.client.FetchPrices(ctx, date)
	if err != nil {
		return nil, err
	}

	sched := scheduler.ComputeSchedule(prices)
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

// priceCache is the on-disk format for persisted price data.
type priceCache struct {
	Today    *DayData `json:"today,omitempty"`
	Tomorrow *DayData `json:"tomorrow,omitempty"`
}

func (f *Fetcher) loadCache() {
	if f.cachePath == "" {
		return
	}
	data, err := os.ReadFile(f.cachePath)
	if err != nil {
		if !os.IsNotExist(err) {
			f.log.Warn("failed to read price cache", slog.Any("error", err))
		}
		return
	}
	var cache priceCache
	if err := json.Unmarshal(data, &cache); err != nil {
		f.log.Warn("failed to parse price cache", slog.Any("error", err))
		return
	}

	now := f.madridNow()
	todayStr := now.Format("2006-01-02")
	tomorrowStr := now.Add(24 * time.Hour).Format("2006-01-02")

	f.mu.Lock()
	defer f.mu.Unlock()

	// Only restore data that is still current
	if cache.Today != nil && cache.Today.Date == todayStr {
		f.state.Today = cache.Today
		f.log.Info("restored today's prices from cache")
	}
	if cache.Tomorrow != nil && cache.Tomorrow.Date == tomorrowStr {
		f.state.Tomorrow = cache.Tomorrow
		f.log.Info("restored tomorrow's prices from cache")
	} else if cache.Tomorrow != nil && cache.Tomorrow.Date == todayStr {
		// Yesterday's "tomorrow" is now today
		f.state.Today = cache.Tomorrow
		f.log.Info("restored today's prices from yesterday's tomorrow cache")
	}
}

func (f *Fetcher) saveCache() {
	if f.cachePath == "" {
		return
	}
	f.mu.RLock()
	cache := priceCache{
		Today:    f.state.Today,
		Tomorrow: f.state.Tomorrow,
	}
	f.mu.RUnlock()

	if cache.Today == nil && cache.Tomorrow == nil {
		return
	}

	data, err := json.Marshal(cache)
	if err != nil {
		f.log.Warn("failed to marshal price cache", slog.Any("error", err))
		return
	}

	if err := os.MkdirAll(filepath.Dir(f.cachePath), 0o755); err != nil {
		f.log.Warn("failed to create cache directory", slog.Any("error", err))
		return
	}

	tmp := f.cachePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		f.log.Warn("failed to write price cache", slog.Any("error", err))
		return
	}
	if err := os.Rename(tmp, f.cachePath); err != nil {
		f.log.Warn("failed to rename price cache", slog.Any("error", err))
	}
}
