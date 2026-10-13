package wsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"gok-pi/internal/lib/sl"
	"gok-pi/internal/netscan"
	"log/slog"
	"time"
)

// This file carries the battery-scan RPC: the control server asks the agent to
// sweep a range of addresses, and the agent answers with what it found.
//
// It answers a question the log cannot. When a battery stops responding the
// agent can only report that it is unreachable; whether the device moved to a
// new DHCP lease or is genuinely off the air is invisible from here, and the two
// want opposite responses. Walking the subnet from the device itself — the only
// place with a route to the battery — settles it.
//
// The work is handed to a scanner the agent wires in (see cmd/gok), because the
// probe needs the driver registry and the battery's token, and neither belongs
// in the transport.

const (
	// scanTimeout caps one sweep. A /24 at the scanner's defaults finishes in a
	// few seconds; the cap is what bounds a pathological case (every address
	// listening, every probe retrying) so the RPC always answers.
	scanTimeout = 90 * time.Second

	// scanReplyWriteTimeout is generous because the reply can be a few hundred
	// results and the uplink that prompted the scan may well be a slow one.
	scanReplyWriteTimeout = 60 * time.Second
)

// BatteryScanRequest asks the agent to look for a battery.
type BatteryScanRequest struct {
	// Battery names the configured battery whose driver and token the probe
	// borrows. Empty means the agent's only battery; with several configured it
	// must be named, since probing with the wrong vendor's client proves nothing.
	Battery string `json:"battery,omitempty"`

	// Range is the address range to sweep, in any form netscan.ParseRange
	// accepts. Empty means the /24 around the battery's configured address,
	// which is where a battery that took a new lease almost always is.
	Range string `json:"range,omitempty"`

	// Port is the TCP port the sweep's first stage connects to. Zero means the
	// port from the battery's configured URL.
	Port int `json:"port,omitempty"`
}

// BatteryScanResult is what the sweep found, plus the parameters it actually
// used — the request may have left them to the agent to work out, and an
// operator reading the result needs to know what was searched.
type BatteryScanResult struct {
	Battery       string `json:"battery"`
	ConfiguredURL string `json:"configured_url,omitempty"`
	Range         string `json:"range"`
	Port          int    `json:"port"`

	Results   []netscan.Result `json:"results"`
	Scanned   int              `json:"scanned"`
	Responded int              `json:"responded"`
	Found     int              `json:"found"`
	ElapsedMs int64            `json:"elapsed_ms"`
	Truncated bool             `json:"truncated,omitempty"`
}

// BatteryScanner performs the sweep. It is injected rather than implemented here
// so the transport stays free of driver and credential knowledge.
type BatteryScanner func(ctx context.Context, req BatteryScanRequest) (BatteryScanResult, error)

// ScanResponse is the reply to a scan request. Like DiagResponse it is a named
// type so the control server's mirror of it can be checked against it in a test.
type ScanResponse struct {
	Type      string             `json:"type"`
	RequestID string             `json:"request_id"`
	Scan      *BatteryScanResult `json:"scan,omitempty"`
	Error     string             `json:"error,omitempty"`
	SentAt    time.Time          `json:"sent_at"`
}

// SetBatteryScanner installs the scanner that answers scan requests. Without one
// the agent refuses the RPC rather than silently reporting an empty sweep, which
// would read as "the battery is nowhere on the subnet". Must be called before Run.
func (c *Client) SetBatteryScanner(fn BatteryScanner) {
	c.batteryScanner = fn
}

// handleScanRequest answers a sweep request.
//
// The sweep runs on its own goroutine: it takes seconds, and the read loop it
// would otherwise block is the same loop that carries config pushes, commands
// and the pong that keeps the connection alive. A scan that stalled the uplink
// while diagnosing the battery would be a poor trade.
func (c *Client) handleScanRequest(ctx context.Context, raw json.RawMessage) {
	var req struct {
		Type      string             `json:"type"`
		RequestID string             `json:"request_id"`
		Scan      BatteryScanRequest `json:"scan"`
		SentAt    time.Time          `json:"sent_at"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		c.log.With(sl.Err(err)).Error("decode battery scan request")
		return
	}

	reply := func(result *BatteryScanResult, errMsg string) {
		c.enqueueReply("battery scan response", ScanResponse{
			Type:      messageTypeScanResponse,
			RequestID: req.RequestID,
			Scan:      result,
			Error:     errMsg,
			SentAt:    time.Now().UTC(),
		}, scanReplyWriteTimeout)
	}

	scanner := c.batteryScanner
	if scanner == nil {
		reply(nil, "battery scan is not available on this agent")
		return
	}

	// One sweep at a time. Two concurrent sweeps would double the traffic on the
	// subnet and, worse, probe the battery twice over — and an operator clicking
	// the button again because the first scan is taking a while is the expected
	// way to get here, not an edge case.
	if !c.scanRunning.CompareAndSwap(false, true) {
		reply(nil, "a battery scan is already running")
		return
	}

	go func() {
		defer c.scanRunning.Store(false)

		scanCtx, cancel := context.WithTimeout(ctx, scanTimeout)
		defer cancel()

		log := c.log.With(
			slog.String("battery", req.Scan.Battery),
			slog.String("range", req.Scan.Range),
		)
		log.Info("battery scan requested")

		result, err := scanner(scanCtx, req.Scan)
		if err != nil {
			log.With(sl.Err(err)).Warn("battery scan failed")
			reply(nil, err.Error())
			return
		}

		log.With(
			slog.String("scanned_range", result.Range),
			slog.Int("scanned", result.Scanned),
			slog.Int("responded", result.Responded),
			slog.Int("found", result.Found),
			slog.Int64("elapsed_ms", result.ElapsedMs),
		).Info("battery scan finished")

		reply(&result, "")
	}()
}

// DeriveScanRange works out the range to sweep when a request did not name one:
// the /24 around the battery's configured host. It is exported because the
// scanner in cmd/gok needs the same rule, and because the error it returns is
// what tells an operator to type a range by hand — a battery configured by
// hostname gives nothing to derive a subnet from.
func DeriveScanRange(host string) (string, error) {
	subnet, err := netscan.SubnetOf(host)
	if err != nil {
		return "", fmt.Errorf("cannot work out a range from %q; give one explicitly: %w", host, err)
	}
	return subnet, nil
}
