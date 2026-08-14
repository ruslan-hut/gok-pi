package wsclient

import (
	"runtime"
	"sync"
	"time"

	"gok-pi/internal/remote/spool"
	"gok-pi/metrics/observers"
)

// Uplink health thresholds.
const (
	// backlogStallAfter is how old the oldest undelivered snapshot may get before
	// the uplink is treated as stuck. Telemetry is appended every 10s and flushed
	// within 5s, so anything approaching this is not congestion — it is a write
	// path that has stopped draining while the connection still looks alive.
	backlogStallAfter = 15 * time.Minute

	// backlogCheckInterval is how often that is evaluated. It runs on the write
	// loop's own ticker, so a stuck flush is caught by the same goroutine that
	// would be doing the flushing.
	backlogCheckInterval = time.Minute
)

// uplinkHealth counts what the telemetry path actually did, so a fault that
// produces no error log still leaves evidence. Every field is written from the
// observer callback or the write loop and read from the heartbeat and the
// diagnostics RPC, so all access goes through the mutex.
type uplinkHealth struct {
	mu sync.Mutex

	startedAt time.Time

	appended      uint64 // snapshots handed to the spool
	appendErrors  uint64
	delivered     uint64 // snapshots written to the socket and marked delivered
	flushErrors   uint64
	markErrors    uint64
	readErrors    uint64
	reconnects    uint64
	forcedRedials uint64 // connections dropped by the backlog watchdog

	lastAppendAt   time.Time
	lastDeliveryAt time.Time
	lastPruneAt    time.Time
	lastPruneCount int64
	lastError      string
	lastErrorAt    time.Time
}

func newUplinkHealth() *uplinkHealth {
	return &uplinkHealth{startedAt: time.Now()}
}

func (h *uplinkHealth) recordAppend(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err != nil {
		h.appendErrors++
		h.setErrorLocked(err)
		return
	}
	h.appended++
	h.lastAppendAt = time.Now()
}

func (h *uplinkHealth) recordDelivered(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.delivered += uint64(n)
	h.lastDeliveryAt = time.Now()
}

func (h *uplinkHealth) recordFlushError(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.flushErrors++
	h.setErrorLocked(err)
}

func (h *uplinkHealth) recordReadError(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.readErrors++
	h.setErrorLocked(err)
}

func (h *uplinkHealth) recordMarkError(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.markErrors++
	h.setErrorLocked(err)
}

func (h *uplinkHealth) recordPrune(deleted int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastPruneAt = time.Now()
	h.lastPruneCount = deleted
}

func (h *uplinkHealth) recordReconnect() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reconnects++
}

func (h *uplinkHealth) recordForcedRedial() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.forcedRedials++
}

func (h *uplinkHealth) setErrorLocked(err error) {
	if err == nil {
		return
	}
	h.lastError = err.Error()
	h.lastErrorAt = time.Now()
}

// SpoolHealth is the compact uplink picture carried on every heartbeat. It is
// small on purpose: it rides a 30s message, and the fields are the ones that
// answer "is telemetry actually getting through" without a round trip.
type SpoolHealth struct {
	Pending                 int64      `json:"pending"`
	OldestUndeliveredAgeSec int64      `json:"oldest_undelivered_age_sec,omitempty"`
	Appended                uint64     `json:"appended"`
	Delivered               uint64     `json:"delivered"`
	FlushErrors             uint64     `json:"flush_errors,omitempty"`
	LastDeliveryAt          *time.Time `json:"last_delivery_at,omitempty"`
	LastError               string     `json:"last_error,omitempty"`
}

// Diagnostics is the full on-demand picture returned by the diagnostics RPC.
type Diagnostics struct {
	Agent      AgentInfo            `json:"agent"`
	UptimeSec  int64                `json:"uptime_sec"`
	Goroutines int                  `json:"goroutines"`
	Spool      *spool.Stats         `json:"spool,omitempty"`
	SpoolError string               `json:"spool_error,omitempty"`
	Uplink     UplinkDiagnostics    `json:"uplink"`
	Batteries  []observers.Snapshot `json:"batteries"`
	Config     ConfigDiagnostics    `json:"config"`
}

// UplinkDiagnostics is the counter set behind SpoolHealth, kept separate so the
// heartbeat stays small while the RPC can be thorough.
type UplinkDiagnostics struct {
	Appended       uint64     `json:"appended"`
	AppendErrors   uint64     `json:"append_errors"`
	Delivered      uint64     `json:"delivered"`
	FlushErrors    uint64     `json:"flush_errors"`
	MarkErrors     uint64     `json:"mark_errors"`
	ReadErrors     uint64     `json:"read_errors"`
	Reconnects     uint64     `json:"reconnects"`
	ForcedRedials  uint64     `json:"forced_redials"`
	LastAppendAt   *time.Time `json:"last_append_at,omitempty"`
	LastDeliveryAt *time.Time `json:"last_delivery_at,omitempty"`
	LastPruneAt    *time.Time `json:"last_prune_at,omitempty"`
	LastPruneCount int64      `json:"last_prune_count"`
	LastError      string     `json:"last_error,omitempty"`
	LastErrorAt    *time.Time `json:"last_error_at,omitempty"`
}

// ConfigDiagnostics reports what configuration the agent believes it is running,
// which is how a config push that silently failed to apply is caught.
type ConfigDiagnostics struct {
	Batteries int `json:"batteries"`
	Schedules int `json:"schedules"`
}

func (h *uplinkHealth) snapshot() UplinkDiagnostics {
	h.mu.Lock()
	defer h.mu.Unlock()

	return UplinkDiagnostics{
		Appended:       h.appended,
		AppendErrors:   h.appendErrors,
		Delivered:      h.delivered,
		FlushErrors:    h.flushErrors,
		MarkErrors:     h.markErrors,
		ReadErrors:     h.readErrors,
		Reconnects:     h.reconnects,
		ForcedRedials:  h.forcedRedials,
		LastAppendAt:   optionalTime(h.lastAppendAt),
		LastDeliveryAt: optionalTime(h.lastDeliveryAt),
		LastPruneAt:    optionalTime(h.lastPruneAt),
		LastPruneCount: h.lastPruneCount,
		LastError:      h.lastError,
		LastErrorAt:    optionalTime(h.lastErrorAt),
	}
}

// spoolHealth builds the heartbeat payload. stats may be nil when the spool is
// disabled or unreadable; the counters are still worth reporting.
func (h *uplinkHealth) spoolHealth(stats *spool.Stats, now time.Time) SpoolHealth {
	h.mu.Lock()
	defer h.mu.Unlock()

	health := SpoolHealth{
		Appended:       h.appended,
		Delivered:      h.delivered,
		FlushErrors:    h.flushErrors,
		LastDeliveryAt: optionalTime(h.lastDeliveryAt),
		LastError:      h.lastError,
	}
	if stats != nil {
		health.Pending = stats.Pending
		if stats.OldestUndelivered != nil {
			health.OldestUndeliveredAgeSec = int64(now.Sub(*stats.OldestUndelivered).Seconds())
		}
	}
	return health
}

func (h *uplinkHealth) uptime(now time.Time) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return now.Sub(h.startedAt)
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	out := t.UTC()
	return &out
}

func goroutineCount() int {
	return runtime.NumGoroutine()
}
