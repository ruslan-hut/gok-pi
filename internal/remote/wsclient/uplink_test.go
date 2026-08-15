package wsclient

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"gok-pi/internal/config"
	"gok-pi/internal/remote/spool"
	"gok-pi/metrics/observers"
)

// TestSpoolKeepsRecordingWhileUplinkIsDown pins the property that turns an outage
// into a replayable gap instead of a permanent hole in the history: telemetry is
// appended for as long as the process lives, whatever the connection is doing.
func TestSpoolKeepsRecordingWhileUplinkIsDown(t *testing.T) {
	sp, err := spool.Open(filepath.Join(t.TempDir(), "spool.db"), newTestLogger())
	if err != nil {
		t.Fatalf("open spool: %v", err)
	}
	defer func() { _ = sp.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A port nothing listens on: every dial fails, so the client never has a
	// connection at any point in this test.
	client := New(config.RemoteControl{ServerURL: "ws://127.0.0.1:1"}, AgentMetadata{ID: "agent-1"}, newTestLogger())
	client.UseSpool(sp)
	client.Run(ctx)

	observers.UpdateStatus("test-battery", "Connected")
	observers.UpdateSoC("test-battery", 42)

	deadline := time.Now().Add(2 * time.Second)
	for {
		stats, err := sp.Stats()
		if err != nil {
			t.Fatalf("spool stats: %v", err)
		}
		if stats.Pending >= 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected snapshots to be spooled while disconnected, pending=%d", stats.Pending)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRunRetriesAfterDialDeadline is the regression guard for the incident this
// package's comments describe: a per-operation deadline is a connection failure,
// not a shutdown signal. The listener accepts TCP but never completes the
// WebSocket handshake, so each dial ends in context.DeadlineExceeded.
func TestRunRetriesAfterDialDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out two dial timeouts")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	accepted := make(chan struct{}, 4)
	go func() {
		// Connections are held open and never answered, so a dial can only end in
		// a deadline; they are closed when the listener goes away with the test.
		var held []net.Conn
		defer func() {
			for _, conn := range held {
				_ = conn.Close()
			}
		}()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, conn)
			select {
			case accepted <- struct{}{}:
			default:
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.RemoteControl{ServerURL: "ws://" + ln.Addr().String()}
	cfg.Reconnect.InitialSeconds = 1
	cfg.Reconnect.MaxSeconds = 1

	client := New(cfg, AgentMetadata{ID: "agent-1"}, newTestLogger())
	client.Run(ctx)

	for i := 0; i < 2; i++ {
		select {
		case <-accepted:
		case <-time.After(defaultDialTimeout + 10*time.Second):
			t.Fatalf("expected a redial after a dial deadline; got %d attempts", i)
		}
	}
}

func TestRepliesAreQueuedForTheWriteLoop(t *testing.T) {
	client := New(config.RemoteControl{}, AgentMetadata{ID: "agent-1"}, newTestLogger())

	raw, err := json.Marshal(map[string]string{"type": messageTypeDiagRequest, "request_id": "req-1"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	client.handleDiagRequest(raw)

	select {
	case msg := <-client.outbound:
		resp, ok := msg.payload.(DiagResponse)
		if !ok {
			t.Fatalf("expected a diagnostics response, got %T", msg.payload)
		}
		if resp.RequestID != "req-1" {
			t.Fatalf("expected request id req-1, got %s", resp.RequestID)
		}
	default:
		t.Fatal("expected the diagnostics reply to be queued for the write loop")
	}
}

// TestReplyQueueDoesNotBlockTheReadLoop: a stuck write path must not stop commands
// and config pushes from being read.
func TestReplyQueueDoesNotBlockTheReadLoop(t *testing.T) {
	client := New(config.RemoteControl{}, AgentMetadata{ID: "agent-1"}, newTestLogger())

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < cap(client.outbound)+5; i++ {
			client.enqueueReply("log response", struct{}{}, time.Second)
		}
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("enqueueReply blocked once the outbound buffer filled up")
	}
}
