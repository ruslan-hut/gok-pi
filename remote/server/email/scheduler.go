package email

import (
	"context"
	"log/slog"
	"time"

	"gok-pi/battery/entity"
	"gok-pi/electricity/pricefetcher"
	"gok-pi/internal/lib/sl"
	"gok-pi/remote/server/sessiondb"
)

// AgentJob describes a single agent the scheduler should consider during a tick.
type AgentJob struct {
	AgentID    string
	DeviceName string
	Timezone   string
	Reports    entity.EmailReportsConfig
}

// AgentLister returns the current list of agents/configs to evaluate. The server
// implements this by reading its ConfigStore, avoiding an import cycle.
type AgentLister func() []AgentJob

// PriceLimitsGetter returns current price limits used to render the daily chart.
type PriceLimitsGetter func() PriceLimits

// Scheduler periodically inspects per-agent EmailReports settings and dispatches
// daily/weekly/monthly reports through the Brevo client.
type Scheduler struct {
	log     *slog.Logger
	brevo   *BrevoClient
	builder *ReportBuilder
	state   *stateStore

	listAgents     AgentLister
	getPriceLimits PriceLimitsGetter

	tick time.Duration
}

// NewScheduler constructs a scheduler. Returns nil if brevo is nil (provider disabled).
func NewScheduler(
	log *slog.Logger,
	brevo *BrevoClient,
	store *sessiondb.Store,
	prices *pricefetcher.Fetcher,
	statePath string,
	listAgents AgentLister,
	getPriceLimits PriceLimitsGetter,
) (*Scheduler, error) {
	if brevo == nil || store == nil || listAgents == nil {
		return nil, nil
	}
	state, err := newStateStore(statePath)
	if err != nil {
		return nil, err
	}
	return &Scheduler{
		log:            log.With(sl.Module("email.scheduler")),
		brevo:          brevo,
		builder:        NewReportBuilder(store, prices),
		state:          state,
		listAgents:     listAgents,
		getPriceLimits: getPriceLimits,
		tick:           60 * time.Second,
	}, nil
}

// Run blocks until ctx is cancelled. It evaluates report dispatch on each tick.
func (s *Scheduler) Run(ctx context.Context) {
	if s == nil {
		return
	}
	s.log.Info("email scheduler started")
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()

	// Run once at startup so a missed tick after a restart can recover.
	s.evaluate(ctx)

	for {
		select {
		case <-ctx.Done():
			s.log.Info("email scheduler stopped")
			return
		case <-ticker.C:
			s.evaluate(ctx)
		}
	}
}

func (s *Scheduler) evaluate(ctx context.Context) {
	for _, job := range s.listAgents() {
		if !job.Reports.Enabled || len(job.Reports.Recipients) == 0 {
			continue
		}
		s.processAgent(ctx, job)
	}
}

func (s *Scheduler) processAgent(ctx context.Context, job AgentJob) {
	now := LocalNow(job.Timezone)
	if now.Hour() != clampHour(job.Reports.SendHour) {
		return
	}

	// Daily — covers the previous local day.
	if job.Reports.Daily {
		yesterday := LocalDay(job.Timezone, now.AddDate(0, 0, -1))
		s.maybeSendDaily(ctx, job, yesterday)
	}

	// Weekly — only on Mondays, covers the previous Mon..Sun.
	if job.Reports.Weekly && now.Weekday() == time.Monday {
		thisMonday := MondayOfWeek(now)
		start := thisMonday.AddDate(0, 0, -7)
		end := thisMonday
		s.maybeSendRange(ctx, job, KindWeekly, start, end)
	}

	// Monthly — only on day 1, covers the previous month.
	if job.Reports.Monthly && now.Day() == 1 {
		thisMonth := FirstOfMonth(now)
		start := thisMonth.AddDate(0, -1, 0)
		end := thisMonth
		s.maybeSendRange(ctx, job, KindMonthly, start, end)
	}
}

func (s *Scheduler) maybeSendDaily(ctx context.Context, job AgentJob, day time.Time) {
	dayISO := day.Format("2006-01-02")
	if s.state.lastSent(job.AgentID, KindDaily) == dayISO {
		return
	}

	limits := PriceLimits{}
	if s.getPriceLimits != nil {
		limits = s.getPriceLimits()
	}

	rep, err := s.builder.BuildDaily(ctx, job.AgentID, job.DeviceName, job.Timezone, day, limits)
	if err != nil {
		s.log.Error("build daily report",
			slog.String("agent", job.AgentID),
			slog.String("date", dayISO),
			sl.Err(err),
		)
		return
	}

	subject, html, err := RenderDaily(rep)
	if err != nil {
		s.log.Error("render daily report",
			slog.String("agent", job.AgentID),
			slog.String("date", dayISO),
			sl.Err(err),
		)
		return
	}

	if err := s.brevo.Send(ctx, job.Reports.Recipients, subject, html); err != nil {
		s.log.Error("send daily report",
			slog.String("agent", job.AgentID),
			slog.String("date", dayISO),
			sl.Err(err),
		)
		return
	}

	if err := s.state.markSent(job.AgentID, KindDaily, dayISO); err != nil {
		s.log.Warn("persist email state after daily send", sl.Err(err))
	}

	s.log.Info("daily email sent",
		slog.String("agent", job.AgentID),
		slog.String("date", dayISO),
		slog.Int("recipients", len(job.Reports.Recipients)),
	)
}

func (s *Scheduler) maybeSendRange(ctx context.Context, job AgentJob, kind ReportKind, start, end time.Time) {
	startISO := start.Format("2006-01-02")
	if s.state.lastSent(job.AgentID, kind) == startISO {
		return
	}

	rep, err := s.builder.BuildRange(job.AgentID, job.DeviceName, job.Timezone, kind, start, end)
	if err != nil {
		s.log.Error("build range report",
			slog.String("agent", job.AgentID),
			slog.String("kind", string(kind)),
			slog.String("start", startISO),
			sl.Err(err),
		)
		return
	}

	subject, html, err := RenderRange(rep)
	if err != nil {
		s.log.Error("render range report",
			slog.String("agent", job.AgentID),
			slog.String("kind", string(kind)),
			sl.Err(err),
		)
		return
	}

	if err := s.brevo.Send(ctx, job.Reports.Recipients, subject, html); err != nil {
		s.log.Error("send range report",
			slog.String("agent", job.AgentID),
			slog.String("kind", string(kind)),
			slog.String("start", startISO),
			sl.Err(err),
		)
		return
	}

	if err := s.state.markSent(job.AgentID, kind, startISO); err != nil {
		s.log.Warn("persist email state after range send", sl.Err(err))
	}

	s.log.Info("range email sent",
		slog.String("agent", job.AgentID),
		slog.String("kind", string(kind)),
		slog.String("start", startISO),
		slog.Int("recipients", len(job.Reports.Recipients)),
	)
}

func clampHour(h int) int {
	switch {
	case h < 0:
		return 0
	case h > 23:
		return 23
	default:
		return h
	}
}
