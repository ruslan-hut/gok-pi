package wsclient

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gok-pi/internal/config"
	"gok-pi/internal/netscan"
)

func newScanClient(t *testing.T) *Client {
	t.Helper()
	return New(config.RemoteControl{}, AgentMetadata{ID: "agent-1", Env: "test"}, newTestLogger())
}

func scanRequestJSON(t *testing.T, req BatteryScanRequest) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]interface{}{
		"type":       messageTypeScanRequest,
		"request_id": "agent-1-scan-1",
		"scan":       req,
		"sent_at":    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("marshal scan request: %v", err)
	}
	return raw
}

// awaitReply takes the next queued reply off the outbound channel. Replies are
// produced on the scan's own goroutine, so the wait is real rather than a
// formality.
func awaitReply(t *testing.T, c *Client) ScanResponse {
	t.Helper()
	select {
	case msg := <-c.outbound:
		resp, ok := msg.payload.(ScanResponse)
		if !ok {
			t.Fatalf("queued reply is %T, want ScanResponse", msg.payload)
		}
		return resp
	case <-time.After(5 * time.Second):
		t.Fatal("no scan reply was queued")
		return ScanResponse{}
	}
}

func TestHandleScanRequestRepliesWithResult(t *testing.T) {
	client := newScanClient(t)

	var gotReq BatteryScanRequest
	client.SetBatteryScanner(func(_ context.Context, req BatteryScanRequest) (BatteryScanResult, error) {
		gotReq = req
		return BatteryScanResult{
			Battery: "BAT-001",
			Range:   "192.168.68.0/24",
			Port:    80,
			Scanned: 254,
			Found:   1,
			Results: []netscan.Result{
				{Address: "192.168.68.142", PortOpen: true, Battery: true, Detail: "sonnen: usoc 61%"},
			},
		}, nil
	})

	client.handleScanRequest(context.Background(), scanRequestJSON(t, BatteryScanRequest{
		Battery: "BAT-001",
		Range:   "192.168.68.0/24",
	}))

	resp := awaitReply(t, client)
	if resp.Type != messageTypeScanResponse {
		t.Errorf("type = %q, want %q", resp.Type, messageTypeScanResponse)
	}
	// The request ID is what correlates the reply with the waiter on the server.
	if resp.RequestID != "agent-1-scan-1" {
		t.Errorf("request_id = %q", resp.RequestID)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %q", resp.Error)
	}
	if resp.Scan == nil || resp.Scan.Found != 1 {
		t.Fatalf("unexpected scan result: %+v", resp.Scan)
	}

	// The request has to reach the scanner intact, or the sweep covers a range
	// nobody asked for.
	if gotReq.Battery != "BAT-001" || gotReq.Range != "192.168.68.0/24" {
		t.Errorf("scanner received %+v", gotReq)
	}
}

func TestHandleScanRequestWithoutScanner(t *testing.T) {
	client := newScanClient(t)

	client.handleScanRequest(context.Background(), scanRequestJSON(t, BatteryScanRequest{}))

	resp := awaitReply(t, client)
	// Refusing explicitly matters: an empty result would read as "the battery is
	// nowhere on the subnet", which is a different and alarming answer.
	if resp.Error == "" {
		t.Fatal("expected a refusal when no scanner is wired in")
	}
	if resp.Scan != nil {
		t.Errorf("expected no scan payload, got %+v", resp.Scan)
	}
}

func TestHandleScanRequestForwardsScannerError(t *testing.T) {
	client := newScanClient(t)
	client.SetBatteryScanner(func(context.Context, BatteryScanRequest) (BatteryScanResult, error) {
		return BatteryScanResult{}, errors.New(`invalid address "nonsense"`)
	})

	client.handleScanRequest(context.Background(), scanRequestJSON(t, BatteryScanRequest{Range: "nonsense"}))

	resp := awaitReply(t, client)
	if !strings.Contains(resp.Error, "nonsense") {
		t.Errorf("error = %q, want the scanner's own message", resp.Error)
	}
	if resp.Scan != nil {
		t.Errorf("expected no scan payload on failure, got %+v", resp.Scan)
	}
}

func TestHandleScanRequestRefusesSecondScan(t *testing.T) {
	client := newScanClient(t)

	started := make(chan struct{})
	release := make(chan struct{})
	client.SetBatteryScanner(func(context.Context, BatteryScanRequest) (BatteryScanResult, error) {
		close(started)
		<-release
		return BatteryScanResult{Battery: "BAT-001"}, nil
	})

	client.handleScanRequest(context.Background(), scanRequestJSON(t, BatteryScanRequest{}))
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first scan never started")
	}

	// An operator clicking the button again because the first scan is taking a
	// while is the expected way to get here, so the second request must be
	// answered rather than silently queued or run in parallel.
	client.handleScanRequest(context.Background(), scanRequestJSON(t, BatteryScanRequest{}))
	second := awaitReply(t, client)
	if !strings.Contains(second.Error, "already running") {
		t.Errorf("second scan error = %q, want it to say one is already running", second.Error)
	}

	close(release)
	first := awaitReply(t, client)
	if first.Error != "" || first.Scan == nil {
		t.Errorf("first scan did not complete: error=%q scan=%+v", first.Error, first.Scan)
	}

	// The latch has to clear, or one slow scan disables the feature until restart.
	client.SetBatteryScanner(func(context.Context, BatteryScanRequest) (BatteryScanResult, error) {
		return BatteryScanResult{Battery: "BAT-001"}, nil
	})
	client.handleScanRequest(context.Background(), scanRequestJSON(t, BatteryScanRequest{}))
	third := awaitReply(t, client)
	if third.Error != "" {
		t.Errorf("a later scan was refused: %q", third.Error)
	}
}

func TestHandleScanRequestDoesNotBlockTheReadLoop(t *testing.T) {
	client := newScanClient(t)

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	client.SetBatteryScanner(func(context.Context, BatteryScanRequest) (BatteryScanResult, error) {
		<-release
		return BatteryScanResult{}, nil
	})

	// handleScanRequest is called from the read loop, which also carries config
	// pushes, commands and the pong that keeps the uplink alive. A sweep takes
	// seconds; blocking there would stall the connection while diagnosing it.
	done := make(chan struct{})
	go func() {
		client.handleScanRequest(context.Background(), scanRequestJSON(t, BatteryScanRequest{}))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleScanRequest blocked on the sweep")
	}
}

func TestHandleScanRequestIgnoresMalformedMessage(t *testing.T) {
	client := newScanClient(t)
	client.SetBatteryScanner(func(context.Context, BatteryScanRequest) (BatteryScanResult, error) {
		t.Error("scanner ran for a message that could not be decoded")
		return BatteryScanResult{}, nil
	})

	client.handleScanRequest(context.Background(), json.RawMessage(`{"type":`))

	select {
	case msg := <-client.outbound:
		t.Fatalf("queued a reply for an undecodable request: %+v", msg.payload)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDeriveScanRange(t *testing.T) {
	got, err := DeriveScanRange("192.168.68.107")
	if err != nil {
		t.Fatalf("DeriveScanRange: %v", err)
	}
	if got != "192.168.68.0/24" {
		t.Errorf("DeriveScanRange = %q, want 192.168.68.0/24", got)
	}

	// A battery configured by hostname gives nothing to derive a subnet from,
	// and the error is what tells the operator to type a range by hand.
	_, err = DeriveScanRange("battery.local")
	if err == nil {
		t.Fatal("DeriveScanRange accepted a hostname")
	}
	if !strings.Contains(err.Error(), "explicitly") {
		t.Errorf("error %q does not tell the operator what to do", err)
	}
}
