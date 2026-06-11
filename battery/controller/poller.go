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
		// Log only on the healthy->failed transition so a sustained outage is
		// logged once rather than every interval.
		if observers.ConnectionFailed(p.name) {
			p.log.With(sl.Err(err)).Error("checking battery status")
		}
		observers.UpdateStatus(p.name, "Disconnected")
		return
	}
	if observers.ConnectionRecovered(p.name) {
		p.log.Info("battery status recovered")
	}
	observers.UpdateStatus(p.name, "Connected")
	p.observe(status)

	for _, ch := range p.subscribers {
		// Keep only the latest reading: drop a stale unread one, then enqueue.
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

// observe pushes battery-level telemetry to the observers. Direction-specific gauges
// (discharge/charge active state) are updated by the controllers from their own feed.
func (p *StatusPoller) observe(status *entity.SystemStatus) {
	if status == nil {
		return
	}
	go func(s *entity.SystemStatus) {
		observers.UpdateSoC(p.name, s.RSOC)
		observers.UpdateUSoC(p.name, s.USOC)
		observers.UpdateCapacity(p.name, s.RemainingCapacityWh)
		observers.UpdateConsumption(p.name, s.ConsumptionW)
		observers.UpdatePac(p.name, s.PacTotalW)
		observers.UpdateDischargeState(p.name, s.BatteryDischarging)
		observers.UpdateChargeState(p.name, s.BatteryCharging)
		observers.UpdateOpMode(p.name, s.OperatingMode)
	}(status)
}
