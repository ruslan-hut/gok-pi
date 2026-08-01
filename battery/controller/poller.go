package controller

import (
	"context"
	"gok-pi/battery/entity"
	"gok-pi/internal/lib/sl"
	"gok-pi/metrics/observers"
	"log/slog"
	"time"
)

// StatusPoller is the single source of battery status for a battery. It polls the
// driver once per interval and fans the result out to every subscribed controller,
// so the discharge controller, charge controller, and status monitor share one HTTP
// request per tick instead of each polling the battery independently.
//
// The poller also owns the battery-level concerns that used to be duplicated across
// pollers: connection-status tracking (with transition-only error logging) and the
// Prometheus/telemetry gauges for the battery as a whole. Direction-specific state is
// left to the controllers via the status delivered on their feed.
type StatusPoller struct {
	name        string
	client      Client
	log         *slog.Logger
	interval    time.Duration
	subscribers []chan *entity.SystemStatus
}

// StaleAfterFailures is how many consecutive failed polls make the last successful
// reading stale. At that point the poller publishes a nil status so consumers stop
// deciding from data they can no longer refresh. It is deliberately small: a single
// failed poll is a blip, but half a minute of silence means the battery may have
// been changed by someone else, or not be there at all.
const StaleAfterFailures = 3

// NewStatusPoller creates a poller for a battery. Subscribe consumers before calling Run.
func NewStatusPoller(name string, client Client, log *slog.Logger) *StatusPoller {
	return &StatusPoller{
		name:     name,
		client:   client,
		log:      log,
		interval: 10 * time.Second,
	}
}

// Subscribe registers a consumer and returns a channel that receives successful status
// readings. Must be called before Run. The channel is buffered with capacity 1; if a
// consumer has not drained the previous reading the poller replaces it with the latest
// rather than blocking, so a slow consumer (e.g. one executing a battery operation)
// never stalls the poller or the other consumers.
func (p *StatusPoller) Subscribe() <-chan *entity.SystemStatus {
	ch := make(chan *entity.SystemStatus, 1)
	p.subscribers = append(p.subscribers, ch)
	return ch
}

// Run polls until the context is cancelled. It performs one poll immediately so
// consumers do not wait a full interval for their first reading.
func (p *StatusPoller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	observers.UpdateStatus(p.name, "Disconnected")
	p.poll()

	for {
		select {
		case <-ctx.Done():
			p.log.Info("battery status poller stopped")
			return
		case <-ticker.C:
			p.poll()
		}
	}
}

func (p *StatusPoller) poll() {
	status, err := p.client.Status()
	if err != nil {
		// Logged on the healthy->failed transition and then periodically while the
		// battery stays unreachable, so an outage that never recovers stays visible
		// instead of leaving a single line and then going silent indefinitely.
		shouldLog, consecutive := observers.ConnectionFailed(p.name)
		if shouldLog {
			p.log.With(
				sl.Err(err),
				slog.Int("consecutive_failures", consecutive),
				slog.Duration("failing_for", time.Duration(consecutive-1)*p.interval),
			).Error("checking battery status")
		}
		observers.UpdateStatus(p.name, "Disconnected")
		if consecutive == StaleAfterFailures {
			// Publish "no reading" once per outage. Without this the last
			// successful status stays on the consumers' side indefinitely and
			// they keep making decisions from data that is minutes old.
			p.publish(nil)
		}
		return
	}
	if recovered, failures := observers.ConnectionRecovered(p.name); recovered {
		p.log.With(slog.Int("failed_polls", failures)).Info("battery status recovered")
	}
	if status == nil {
		// Defensive: a driver returning (nil, nil) is still reachable, but there
		// is no reading to publish.
		observers.UpdateStatus(p.name, "Connected")
	} else {
		p.observe(status)
	}

	p.publish(status)
}

// publish fans a reading out to every subscriber, keeping only the latest: a stale
// unread reading is dropped before enqueueing. A nil reading means "the battery is
// unreachable and the last one is no longer trustworthy".
func (p *StatusPoller) publish(status *entity.SystemStatus) {
	for _, ch := range p.subscribers {
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- status:
		default:
		}
	}
}

// observe pushes battery-level telemetry to the observers. The whole reading goes
// out as a single batched update: notifying per field would emit one telemetry
// snapshot per field and multiply spool writes and uplink traffic accordingly.
// The update is synchronous so a later "Disconnected" can never overtake it.
func (p *StatusPoller) observe(status *entity.SystemStatus) {
	observers.UpdateBattery(p.name, observers.Reading{
		RSOC:                status.RSOC,
		USOC:                status.USOC,
		RemainingCapacityWh: status.RemainingCapacityWh,
		ConsumptionW:        status.ConsumptionW,
		PacTotalW:           status.PacTotalW,
		BatteryDischarging:  status.BatteryDischarging,
		BatteryCharging:     status.BatteryCharging,
		OperatingMode:       status.OperatingMode,
		Status:              "Connected",
	})
}
