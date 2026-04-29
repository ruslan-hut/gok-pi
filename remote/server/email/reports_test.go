package email

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"gok-pi/remote/server/sessiondb"
)

func openTestStore(t *testing.T) *sessiondb.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := sessiondb.Open(filepath.Join(dir, "sessions.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("open sessiondb: %v", err)
	}
	t.Cleanup(func() { _ = store })
	return store
}

func seedClosedSession(t *testing.T, store *sessiondb.Store, agentID, battery, kind string, started time.Time, durationMin int, energyWh, costEur float64) {
	t.Helper()
	rec := &sessiondb.SessionRecord{
		AgentID:     agentID,
		BatteryName: battery,
		Type:        kind,
		StartedAt:   started.UTC(),
		SocStart:    50,
	}
	id, err := store.InsertSession(rec)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	end := started.Add(time.Duration(durationMin) * time.Minute)
	if err := store.CloseSession(id, end, float64(durationMin)*60, energyWh, energyWh*60/float64(durationMin), energyWh*60/float64(durationMin), 60, 100, costEur, 30); err != nil {
		t.Fatalf("close session: %v", err)
	}
}

func TestBuildDailyAggregates(t *testing.T) {
	store := openTestStore(t)
	b := NewReportBuilder(store, nil)

	tz := "Europe/Madrid"
	loc, _ := time.LoadLocation(tz)
	day := time.Date(2026, 4, 28, 0, 0, 0, 0, loc)

	seedClosedSession(t, store, "agent-1", "bat-A", "discharge", day.Add(8*time.Hour), 60, 1500, 0.45)
	seedClosedSession(t, store, "agent-1", "bat-A", "charge", day.Add(2*time.Hour), 60, 2000, -0.30)
	// Different battery, same day
	seedClosedSession(t, store, "agent-1", "bat-B", "discharge", day.Add(20*time.Hour), 30, 800, 0.20)
	// Other agent — should be excluded
	seedClosedSession(t, store, "agent-2", "bat-X", "discharge", day.Add(10*time.Hour), 60, 1000, 0.50)
	// Previous day — should be excluded
	seedClosedSession(t, store, "agent-1", "bat-A", "discharge", day.Add(-2*time.Hour), 30, 500, 0.10)

	rep, err := b.BuildDaily(context.Background(), "agent-1", "MyHome", tz, day, PriceLimits{})
	if err != nil {
		t.Fatalf("BuildDaily: %v", err)
	}

	if len(rep.Sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(rep.Sessions))
	}
	// Sessions must be sorted ascending by start time
	for i := 1; i < len(rep.Sessions); i++ {
		if rep.Sessions[i].StartedAt.Before(rep.Sessions[i-1].StartedAt) {
			t.Fatalf("sessions not ordered ascending")
		}
	}

	if len(rep.Summaries) != 2 {
		t.Fatalf("expected 2 batteries in summaries, got %d", len(rep.Summaries))
	}
	// Summaries sorted by battery name
	if rep.Summaries[0].BatteryName != "bat-A" || rep.Summaries[1].BatteryName != "bat-B" {
		t.Fatalf("summaries not sorted: %+v", rep.Summaries)
	}

	// Verify aggregate totals for bat-A
	a := rep.Summaries[0]
	if a.ChargeEnergyWh != 2000 || a.DischargeEnergyWh != 1500 {
		t.Fatalf("bat-A energy mismatch: %+v", a)
	}
	if a.ChargeCostEur != -0.30 || a.DischargeCostEur != 0.45 {
		t.Fatalf("bat-A cost mismatch: %+v", a)
	}
	if diff := a.NetCostEur - 0.15; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("bat-A net mismatch: got %v want 0.15", a.NetCostEur)
	}
}

func TestBuildRangeWeeklyAndMonthly(t *testing.T) {
	store := openTestStore(t)
	b := NewReportBuilder(store, nil)
	tz := "UTC"
	loc, _ := time.LoadLocation(tz)

	// April 2026 — seed sessions across multiple days
	seedClosedSession(t, store, "agent-1", "bat-A", "discharge", time.Date(2026, 4, 1, 10, 0, 0, 0, loc), 60, 1000, 0.50)
	seedClosedSession(t, store, "agent-1", "bat-A", "charge", time.Date(2026, 4, 5, 2, 0, 0, 0, loc), 60, 2000, -0.40)
	seedClosedSession(t, store, "agent-1", "bat-A", "discharge", time.Date(2026, 4, 28, 19, 0, 0, 0, loc), 30, 500, 0.25)

	// Weekly: Mon 2026-04-20 through Sun 2026-04-26 — should include zero matching above
	wstart := time.Date(2026, 4, 20, 0, 0, 0, 0, loc)
	wend := wstart.AddDate(0, 0, 7)
	w, err := b.BuildRange("agent-1", "MyHome", tz, KindWeekly, wstart, wend)
	if err != nil {
		t.Fatalf("BuildRange weekly: %v", err)
	}
	if len(w.Sessions) != 0 {
		t.Fatalf("weekly: expected 0 sessions, got %d", len(w.Sessions))
	}

	// Monthly: April 2026 — should include all 3
	mstart := time.Date(2026, 4, 1, 0, 0, 0, 0, loc)
	mend := time.Date(2026, 5, 1, 0, 0, 0, 0, loc)
	m, err := b.BuildRange("agent-1", "MyHome", tz, KindMonthly, mstart, mend)
	if err != nil {
		t.Fatalf("BuildRange monthly: %v", err)
	}
	// Monthly omits Sessions field
	if len(m.Sessions) != 0 {
		t.Fatalf("monthly: expected sessions to be empty (aggregate-only), got %d", len(m.Sessions))
	}
	if len(m.Summaries) != 1 {
		t.Fatalf("monthly: expected 1 battery summary, got %d", len(m.Summaries))
	}
	a := m.Summaries[0]
	if a.ChargeEnergyWh != 2000 || a.DischargeEnergyWh != 1500 {
		t.Fatalf("monthly aggregate mismatch: %+v", a)
	}
	if a.NetCostEur < 0.34 || a.NetCostEur > 0.36 {
		t.Fatalf("monthly net cost mismatch: got %v want ~0.35", a.NetCostEur)
	}
}

func TestMondayOfWeekAndFirstOfMonth(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Madrid")
	cases := []struct {
		t    time.Time
		want time.Time
	}{
		{time.Date(2026, 4, 29, 13, 0, 0, 0, loc), time.Date(2026, 4, 27, 0, 0, 0, 0, loc)}, // Wed → Mon
		{time.Date(2026, 4, 27, 0, 0, 0, 0, loc), time.Date(2026, 4, 27, 0, 0, 0, 0, loc)},  // Mon → Mon
		{time.Date(2026, 5, 3, 23, 59, 0, 0, loc), time.Date(2026, 4, 27, 0, 0, 0, 0, loc)}, // Sun → previous Mon
	}
	for _, c := range cases {
		got := MondayOfWeek(c.t)
		if !got.Equal(c.want) {
			t.Fatalf("MondayOfWeek(%v) = %v, want %v", c.t, got, c.want)
		}
	}

	first := FirstOfMonth(time.Date(2026, 4, 29, 13, 0, 0, 0, loc))
	if !first.Equal(time.Date(2026, 4, 1, 0, 0, 0, 0, loc)) {
		t.Fatalf("FirstOfMonth wrong: %v", first)
	}
}
