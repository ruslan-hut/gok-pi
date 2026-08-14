package server

import (
	"log/slog"
	"sync"
	"time"

	"gok-pi/remote/server/sessiondb"
)

// Telemetry recorder tuning.
const (
	// historyBucket is the resolution of the stored history. Agents poll every 10s,
	// so a minute is six frames: fine enough for an operator to see a discharge
	// ramp, coarse enough that a year of history stays small.
	historyBucket = time.Minute

	// telemetryStallAfter is how long a connected agent may go without telemetry
	// before it is reported as stalled. The agent's own heartbeat is 30s and keeps
	// lastSeen fresh, so without this check a silent telemetry stream is invisible:
	// the agent keeps looking perfectly healthy in the UI.
	telemetryStallAfter = 90 * time.Second

	// telemetryTick is how often buckets are flushed and stalls re-evaluated.
	telemetryTick = 30 * time.Second

	// historyRetention is how long minute buckets are kept.
	historyRetention = 90 * 24 * time.Hour
)

// streamKey identifies one telemetry stream: a single battery on a single agent.
type streamKey struct {
	agentID string
	battery string
}

// telemetryStream is the in-progress minute bucket for one battery's stream.
type telemetryStream struct {
	bucket  time.Time // minute this aggregate covers (UTC)
	samples int

	consumptionSum float64
	consumptionMax float64
	pacSum         float64
	pacMin         float64
	pacMax         float64

	usoc          float64
	rsoc          float64
	capacityWh    float64
	operatingMode string
}

// telemetryRecorder aggregates incoming telemetry into minute buckets and persists
// them as operator-facing history. Whether a stream is still arriving is the
// connection's business, not the recorder's — see agentConnection.evaluateTelemetryStall.
//
// It deliberately does not write on every frame: at 6 frames per minute per battery
// that would be one transaction per 10s forever. Frames accumulate in memory and the
// server's ticker flushes them, which also lets the in-progress minute be upserted
// repeatedly and corrected as it fills.
type telemetryRecorder struct {
	log   *slog.Logger
	store *sessiondb.Store

	mu      sync.Mutex
	streams map[streamKey]*telemetryStream
}

func newTelemetryRecorder(log *slog.Logger, store *sessiondb.Store) *telemetryRecorder {
	return &telemetryRecorder{
		log:     log.With(slog.String("component", "telemetry-history")),
		store:   store,
		streams: make(map[streamKey]*telemetryStream),
	}
}

// Observe folds one telemetry frame into its minute bucket. The bucket comes from
// the snapshot's own timestamp so telemetry replayed from an agent's offline spool
// lands in the minute it was recorded rather than the minute it arrived; now is only
// the fallback for a snapshot that carries no timestamp.
func (t *telemetryRecorder) Observe(agentID string, snap TelemetrySnapshot, now time.Time) {
	if t == nil || agentID == "" || snap.Name == "" {
		return
	}

	recordedAt := snap.UpdatedAt.UTC()
	if recordedAt.IsZero() {
		recordedAt = now.UTC()
	}
	bucket := recordedAt.Truncate(historyBucket)

	key := streamKey{agentID: agentID, battery: snap.Name}

	t.mu.Lock()
	stream, ok := t.streams[key]
	if !ok {
		stream = &telemetryStream{bucket: bucket}
		t.streams[key] = stream
	}

	// A frame for a different minute closes the current one. Frames arrive in order
	// (live or replayed), so the previous bucket is complete and can be written out.
	var completed []sessiondb.TelemetryPoint
	if !stream.bucket.Equal(bucket) {
		if stream.samples > 0 {
			completed = append(completed, stream.point(key))
		}
		stream.reset(bucket)
	}

	stream.accumulate(snap)
	t.mu.Unlock()

	t.persist(completed)
}

// Flush writes every stream's current aggregate. Buckets older than the minute in
// progress are complete, so they are written and dropped; the in-progress bucket is
// upserted too, so the history is at most one tick behind live.
func (t *telemetryRecorder) Flush(now time.Time) {
	if t == nil {
		return
	}
	current := now.UTC().Truncate(historyBucket)

	t.mu.Lock()
	points := make([]sessiondb.TelemetryPoint, 0, len(t.streams))
	for key, stream := range t.streams {
		if stream.samples == 0 {
			continue
		}
		points = append(points, stream.point(key))
		if stream.bucket.Before(current) {
			stream.reset(current)
		}
	}
	t.mu.Unlock()

	t.persist(points)
}

// Forget drops an agent's streams once it disconnects, so a reconnect starts clean
// and a decommissioned agent does not linger in memory. Pending aggregates are
// flushed first: they are real minutes that happened.
func (t *telemetryRecorder) Forget(agentID string) {
	if t == nil {
		return
	}

	t.mu.Lock()
	var points []sessiondb.TelemetryPoint
	for key, stream := range t.streams {
		if key.agentID != agentID {
			continue
		}
		if stream.samples > 0 {
			points = append(points, stream.point(key))
		}
		delete(t.streams, key)
	}
	t.mu.Unlock()

	t.persist(points)
}

func (t *telemetryRecorder) persist(points []sessiondb.TelemetryPoint) {
	if len(points) == 0 || t.store == nil {
		return
	}
	if err := t.store.UpsertTelemetryPoints(points); err != nil {
		t.log.With(slog.Any("error", err), slog.Int("points", len(points))).Warn("persist telemetry history")
	}
}

// accumulate folds one frame into the bucket. Levels (SoC, capacity, mode) are
// last-write-wins because the value at the end of the minute is what an operator
// reads off a chart; power is averaged with its extremes kept.
func (s *telemetryStream) accumulate(snap TelemetrySnapshot) {
	if s.samples == 0 {
		s.pacMin = snap.PacTotalW
		s.pacMax = snap.PacTotalW
	} else {
		if snap.PacTotalW < s.pacMin {
			s.pacMin = snap.PacTotalW
		}
		if snap.PacTotalW > s.pacMax {
			s.pacMax = snap.PacTotalW
		}
	}

	s.consumptionSum += snap.ConsumptionW
	if snap.ConsumptionW > s.consumptionMax {
		s.consumptionMax = snap.ConsumptionW
	}
	s.pacSum += snap.PacTotalW

	s.usoc = snap.USOC
	s.rsoc = snap.RSOC
	s.capacityWh = snap.RemainingCapacityWh
	if snap.OperatingModeSet {
		s.operatingMode = snap.OperatingMode
	}
	s.samples++
}

func (s *telemetryStream) point(key streamKey) sessiondb.TelemetryPoint {
	samples := float64(s.samples)
	return sessiondb.TelemetryPoint{
		AgentID:             key.agentID,
		BatteryName:         key.battery,
		Bucket:              s.bucket.Format(time.RFC3339),
		USOC:                s.usoc,
		RSOC:                s.rsoc,
		RemainingCapacityWh: s.capacityWh,
		ConsumptionAvgW:     s.consumptionSum / samples,
		ConsumptionMaxW:     s.consumptionMax,
		PacAvgW:             s.pacSum / samples,
		PacMinW:             s.pacMin,
		PacMaxW:             s.pacMax,
		OperatingMode:       s.operatingMode,
		Samples:             s.samples,
	}
}

// reset starts a new bucket.
func (s *telemetryStream) reset(bucket time.Time) {
	s.bucket = bucket
	s.samples = 0
	s.consumptionSum = 0
	s.consumptionMax = 0
	s.pacSum = 0
	s.pacMin = 0
	s.pacMax = 0
}
