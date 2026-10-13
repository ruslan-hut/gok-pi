package server

import (
	"encoding/json"
	"testing"
	"time"

	"gok-pi/internal/netscan"
	"gok-pi/internal/remote/wsclient"
	"gok-pi/internal/sysinfo"
)

// The agent and the server declare the messages they exchange separately, on
// purpose: an older server must tolerate a newer agent adding fields rather than
// failing to decode the message. The cost of that choice is that nothing but the
// JSON field names holds the two sides together, and a rename on one side would
// fail silently — the server would simply see zeros and report a healthy uplink
// for an agent that is drowning.
//
// These tests marshal what the agent really sends and unmarshal it into what the
// server really reads, so that drift fails here instead of in production.

func TestHeartbeatSpoolHealthSurvivesTheWire(t *testing.T) {
	lastDelivery := time.Date(2026, 8, 14, 9, 15, 0, 0, time.UTC)
	sent := wsclient.SpoolHealth{
		Pending:                 4212,
		OldestUndeliveredAgeSec: 32400,
		Appended:                8600,
		Delivered:               4388,
		FlushErrors:             7,
		LastDeliveryAt:          &lastDelivery,
		LastError:               "send telemetry: context deadline exceeded",
	}

	raw, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal agent spool health: %v", err)
	}

	var received AgentSpoolHealth
	if err := json.Unmarshal(raw, &received); err != nil {
		t.Fatalf("unmarshal into the server type: %v", err)
	}

	if received.Pending != sent.Pending {
		t.Errorf("pending: sent %d, received %d", sent.Pending, received.Pending)
	}
	if received.OldestUndeliveredAgeSec != sent.OldestUndeliveredAgeSec {
		t.Errorf("oldest_undelivered_age_sec: sent %d, received %d",
			sent.OldestUndeliveredAgeSec, received.OldestUndeliveredAgeSec)
	}
	if received.Appended != sent.Appended || received.Delivered != sent.Delivered {
		t.Errorf("counters: sent %d/%d, received %d/%d",
			sent.Appended, sent.Delivered, received.Appended, received.Delivered)
	}
	if received.FlushErrors != sent.FlushErrors {
		t.Errorf("flush_errors: sent %d, received %d", sent.FlushErrors, received.FlushErrors)
	}
	if received.LastError != sent.LastError {
		t.Errorf("last_error: sent %q, received %q", sent.LastError, received.LastError)
	}
	if received.LastDeliveryAt == nil || !received.LastDeliveryAt.Equal(lastDelivery) {
		t.Errorf("last_delivery_at: sent %v, received %v", lastDelivery, received.LastDeliveryAt)
	}
}

// The backlog warning and the UI badge both key off oldest_undelivered_age_sec,
// so a zero there is the difference between "healthy" and "nine hours behind".
func TestHeartbeatEnvelopeCarriesSpoolHealth(t *testing.T) {
	raw, err := json.Marshal(map[string]interface{}{
		"type":      "agent.heartbeat",
		"timestamp": time.Now().UTC(),
		"agent":     wsclient.AgentInfo{ID: "agent-1", Env: "prod"},
		"spool":     wsclient.SpoolHealth{Pending: 12, OldestUndeliveredAgeSec: 900},
	})
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}

	var heartbeat AgentHeartbeat
	if err := json.Unmarshal(raw, &heartbeat); err != nil {
		t.Fatalf("unmarshal heartbeat: %v", err)
	}
	if heartbeat.Agent.ID != "agent-1" {
		t.Fatalf("expected the agent identity to survive, got %q", heartbeat.Agent.ID)
	}
	if heartbeat.Spool == nil {
		t.Fatal("expected spool health on the heartbeat envelope")
	}
	if heartbeat.Spool.Pending != 12 || heartbeat.Spool.OldestUndeliveredAgeSec != 900 {
		t.Fatalf("unexpected spool health: %+v", heartbeat.Spool)
	}
}

// An agent built before the spool fields existed sends no "spool" key at all.
// That must decode cleanly rather than erroring the whole heartbeat.
func TestHeartbeatFromOlderAgentDecodes(t *testing.T) {
	raw := []byte(`{"type":"agent.heartbeat","timestamp":"2026-08-14T09:00:00Z","agent":{"id":"agent-1"}}`)

	var heartbeat AgentHeartbeat
	if err := json.Unmarshal(raw, &heartbeat); err != nil {
		t.Fatalf("expected an older agent's heartbeat to decode, got %v", err)
	}
	if heartbeat.Spool != nil {
		t.Fatal("expected no spool health from an agent that does not report it")
	}
}

func TestDiagResponseSurvivesTheWire(t *testing.T) {
	sent := wsclient.DiagResponse{
		Type:      "agent.diag.response",
		RequestID: "agent-1-diag-123",
		SentAt:    time.Now().UTC(),
		Diag: wsclient.Diagnostics{
			Agent:      wsclient.AgentInfo{ID: "agent-1", Version: "v1.2.3"},
			UptimeSec:  3600,
			Goroutines: 24,
		},
	}

	raw, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal diagnostics response: %v", err)
	}

	var received AgentDiagResponse
	if err := json.Unmarshal(raw, &received); err != nil {
		t.Fatalf("unmarshal into the server type: %v", err)
	}

	// The request ID is what correlates the reply with its waiter: lose it and the
	// endpoint times out on an answer that already arrived.
	if received.RequestID != sent.RequestID {
		t.Fatalf("request_id: sent %q, received %q", sent.RequestID, received.RequestID)
	}
	if len(received.Diagnostics) == 0 {
		t.Fatal("expected the diagnostics payload to be carried through")
	}

	// The server forwards the payload verbatim, so this is what reaches the caller.
	var payload struct {
		UptimeSec  int64 `json:"uptime_sec"`
		Goroutines int   `json:"goroutines"`
	}
	if err := json.Unmarshal(received.Diagnostics, &payload); err != nil {
		t.Fatalf("decode forwarded diagnostics: %v", err)
	}
	if payload.UptimeSec != 3600 || payload.Goroutines != 24 {
		t.Fatalf("unexpected forwarded payload: %+v", payload)
	}
}

// TestBoardHealthSurvivesTheWire pins the JSON names shared by the agent's
// sysinfo.Board and the server's AgentBoardHealth, which are separate
// declarations held together only by those names.
func TestBoardHealthSurvivesTheWire(t *testing.T) {
	temp := 61.5
	sent := sysinfo.Board{
		TempC: &temp,
		Throttled: &sysinfo.Throttled{
			Raw:                  0x50005,
			UnderVoltage:         true,
			Throttled:            true,
			UnderVoltageOccurred: true,
			ThrottledOccurred:    true,
		},
	}

	raw, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal board: %v", err)
	}
	var received AgentBoardHealth
	if err := json.Unmarshal(raw, &received); err != nil {
		t.Fatalf("unmarshal into the server type: %v", err)
	}

	if received.TempC == nil || *received.TempC != temp {
		t.Fatalf("temp_c: got %v", received.TempC)
	}
	want := AgentBoardThrottled(*sent.Throttled)
	if received.Throttled == nil || *received.Throttled != want {
		t.Fatalf("throttled: sent %+v, received %+v", want, received.Throttled)
	}
}

// TestScanResponseSurvivesTheWire pins the JSON names shared by the agent's
// wsclient.ScanResponse and the server's AgentScanResponse. The scan payload is
// forwarded verbatim to the UI, so a rename on either side would show up as an
// empty result panel — a scan that found nothing and a scan whose result the
// server could not read look identical to an operator.
func TestScanResponseSurvivesTheWire(t *testing.T) {
	sent := wsclient.ScanResponse{
		Type:      "agent.scan.response",
		RequestID: "agent-1-scan-123",
		SentAt:    time.Now().UTC(),
		Scan: &wsclient.BatteryScanResult{
			Battery:       "BAT-001",
			ConfiguredURL: "http://192.168.68.107/api/v2",
			Range:         "192.168.68.0/24",
			Port:          80,
			Scanned:       254,
			Responded:     2,
			Found:         1,
			ElapsedMs:     6100,
			Results: []netscan.Result{
				{Address: "192.168.68.142", PortOpen: true, Battery: true, Detail: "sonnen: usoc 61%"},
				{Address: "192.168.68.107", PortOpen: true, Detail: "received status code: 404"},
			},
		},
	}

	raw, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal scan response: %v", err)
	}

	var received AgentScanResponse
	if err := json.Unmarshal(raw, &received); err != nil {
		t.Fatalf("unmarshal into the server type: %v", err)
	}

	if received.RequestID != sent.RequestID {
		t.Fatalf("request_id: sent %q, received %q", sent.RequestID, received.RequestID)
	}
	if received.Error != "" {
		t.Fatalf("unexpected error on a successful scan: %q", received.Error)
	}
	if len(received.Scan) == 0 {
		t.Fatal("expected the scan payload to be carried through")
	}

	// This is the shape the UI reads, since the server forwards it unparsed.
	var payload struct {
		Range   string `json:"range"`
		Found   int    `json:"found"`
		Results []struct {
			Address string `json:"address"`
			Battery bool   `json:"battery"`
			Detail  string `json:"detail"`
		} `json:"results"`
	}
	if err := json.Unmarshal(received.Scan, &payload); err != nil {
		t.Fatalf("decode forwarded scan: %v", err)
	}
	if payload.Range != "192.168.68.0/24" || payload.Found != 1 {
		t.Fatalf("unexpected forwarded payload: %+v", payload)
	}
	if len(payload.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(payload.Results))
	}
	if !payload.Results[0].Battery || payload.Results[0].Address != "192.168.68.142" {
		t.Errorf("the found battery did not survive: %+v", payload.Results[0])
	}
	// The non-battery entry is the one that names what took the old address.
	if payload.Results[1].Detail == "" {
		t.Error("expected the listening non-battery to keep its detail")
	}
}

// TestScanResponseCarriesAgentRejection pins the other half: when the agent
// refuses the request the reason has to reach the operator, because every one of
// those reasons is something they can fix.
func TestScanResponseCarriesAgentRejection(t *testing.T) {
	sent := wsclient.ScanResponse{
		Type:      "agent.scan.response",
		RequestID: "agent-1-scan-124",
		Error:     "a battery scan is already running",
		SentAt:    time.Now().UTC(),
	}

	raw, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal scan response: %v", err)
	}

	var received AgentScanResponse
	if err := json.Unmarshal(raw, &received); err != nil {
		t.Fatalf("unmarshal into the server type: %v", err)
	}
	if received.Error != sent.Error {
		t.Fatalf("error: sent %q, received %q", sent.Error, received.Error)
	}
	if len(received.Scan) != 0 {
		t.Errorf("expected no scan payload on a rejection, got %s", received.Scan)
	}
}
