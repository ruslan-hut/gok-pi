package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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

	// A report is produced when somebody is subscribed to it. The subscriptions
	// are the only switch: an agent-level toggle on top of them meant a report
	// could be silently withheld from a recipient who had asked for it.

	// Daily — covers the previous local day.
	if len(job.Reports.RecipientsFor(entity.EmailKindDaily)) > 0 {
		yesterday := LocalDay(job.Timezone, now.AddDate(0, 0, -1))
		s.maybeSendDaily(ctx, job, yesterday)
	}

	// Weekly — only on Mondays, covers the previous Mon..Sun.
	if now.Weekday() == time.Monday && len(job.Reports.RecipientsFor(entity.EmailKindWeekly)) > 0 {
		thisMonday := MondayOfWeek(now)
		start := thisMonday.AddDate(0, 0, -7)
		end := thisMonday
		s.maybeSendRange(ctx, job, KindWeekly, start, end)
	}

	// Monthly — only on day 1, covers the previous month.
	if now.Day() == 1 && len(job.Reports.RecipientsFor(entity.EmailKindMonthly)) > 0 {
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

	recipients := job.Reports.RecipientsFor(entity.EmailKindDaily)
	if err := s.brevo.Send(ctx, recipients, subject, html); err != nil {
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
		slog.Int("recipients", len(recipients)),
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

	recipients := job.Reports.RecipientsFor(recipientKind(kind))
	if err := s.brevo.Send(ctx, recipients, subject, html); err != nil {
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
		slog.Int("recipients", len(recipients)),
	)
}

// SendTestDaily renders and sends a "[TEST]" daily report for yesterday in the
// agent's local timezone, immediately, without consulting or mutating the
// last-sent state file. Used by the UI's "Send test email" button.
func (s *Scheduler) SendTestDaily(ctx context.Context, job AgentJob, limits PriceLimits) error {
	if s == nil || s.brevo == nil {
		return errors.New("email scheduler unavailable")
	}
	recipients := job.Reports.RecipientsFor(entity.EmailKindDaily)
	if len(recipients) == 0 {
		return errors.New("no recipients")
	}

	day := LocalDay(job.Timezone, LocalNow(job.Timezone).AddDate(0, 0, -1))

	rep, err := s.builder.BuildDaily(ctx, job.AgentID, job.DeviceName, job.Timezone, day, limits)
	if err != nil {
		return fmt.Errorf("build daily: %w", err)
	}

	subject, html, err := RenderDaily(rep)
	if err != nil {
		return fmt.Errorf("render daily: %w", err)
	}
	if !strings.HasPrefix(subject, "[TEST] ") {
		subject = "[TEST] " + subject
	}

	if err := s.brevo.Send(ctx, recipients, subject, html); err != nil {
		return err
	}

	s.log.Info("test daily email sent",
		slog.String("agent", job.AgentID),
		slog.String("date", day.Format("2006-01-02")),
		slog.Int("recipients", len(recipients)),
	)
	return nil
}

// EmailReportsAdapter builds a lightweight EmailReportsConfig for the test endpoint
// from a possibly-nil stored config plus an explicit recipient list. The toggles
// in the stored config are preserved (or default to daily=true if absent) so the
// AgentJob value is consistent with how the scheduler treats real jobs.
func EmailReportsAdapter(stored *entity.EmailReportsConfig, recipients []string) entity.EmailReportsConfig {
	// The test always sends a daily report, so the explicit addresses are
	// subscribed to daily whatever they are subscribed to in the stored config.
	subscribed := make([]entity.EmailRecipient, 0, len(recipients))
	for _, addr := range recipients {
		subscribed = append(subscribed, entity.EmailRecipient{Address: addr, Daily: true})
	}
	if stored == nil {
		return entity.EmailReportsConfig{
			Enabled:    true,
			Recipients: subscribed,
			Daily:      true,
		}
	}
	out := *stored
	out.Recipients = subscribed
	return out
}

// recipientKind maps a report kind to the subscription it is delivered under.
func recipientKind(kind ReportKind) string {
	switch kind {
	case KindWeekly:
		return entity.EmailKindWeekly
	case KindMonthly:
		return entity.EmailKindMonthly
	default:
		return entity.EmailKindDaily
	}
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
