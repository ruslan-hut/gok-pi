package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testChargerToken = "s3cret"

// newChargerTestServer builds a server with the charger integration enabled and
// its state files inside the test's temp dir, plus a fake connected agent whose
// outbound commands the test can inspect.
// chargerTempDir is t.TempDir with best-effort cleanup. New() opens the session
// SQLite database and nothing closes it before the test ends, and Windows refuses
// to delete a file that is still open — t.TempDir would fail the test on that
// alone, after every assertion had passed.
func chargerTempDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "charger-test-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newChargerTestServer(t *testing.T, links []ChargerLink) (*Server, *agentConnection) {
	t.Helper()

	dir := chargerTempDir(t)
	srv := New(Config{
		ChargerWebhookToken: testChargerToken,
		ChargerLinks:        filepath.Join(dir, "charger-links.json"),
		ChargerSessions:     filepath.Join(dir, "charger-sessions.json"),
		ConfigStore:         filepath.Join(dir, "agent-configs.json"),
		SessionDB:           filepath.Join(dir, "sessions.db"),
	}, testLogger())

	if len(links) > 0 {
		if err := srv.chargerLinks.Set(links); err != nil {
			t.Fatalf("set links: %v", err)
		}
	}

	agent := &agentConnection{
		id:   "agent-1",
		s:    srv,
		send: make(chan interface{}, 32),
		done: make(chan struct{}),
	}
	srv.agentsMu.Lock()
	srv.agents[agent.id] = agent
	srv.agentsMu.Unlock()

	return srv, agent
}

func testLink() ChargerLink {
	return ChargerLink{
		Name:           "site-a",
		Enabled:        true,
		LocationId:     "loc-01",
		ChargePointIds: []string{"Wallbox3"},
		AgentId:        "agent-1",
		BatteryName:    "battery1",
		PowerLimit:     3000,
		SocLimit:       40,
		MaxDurationMin: 120,
	}
}

// sentCommands drains everything the agent has been sent so far.
func sentCommands(t *testing.T, agent *agentConnection) []OutgoingCommand {
	t.Helper()

	var out []OutgoingCommand
	for {
		select {
		case msg := <-agent.send:
			cmd, ok := msg.(OutgoingCommand)
			if !ok {
				t.Fatalf("unexpected message type %T on agent channel", msg)
			}
			out = append(out, cmd)
		default:
			return out
		}
	}
}

func webhookBody(eventType, eventID string, txID int, locationID string) []byte {
	body := fmt.Sprintf(`{
		"id": %q, "type": %q, "source": "evsys", "sequence": 1,
		"time": "2026-08-04T10:00:00Z",
		"data": {"charge_point_id": "Wallbox3", "connector_id": 1, "location_id": %q,
		         "transaction_id": %d, "id_tag": "TAG1"}
	}`, eventID, eventType, locationID, txID)
	return []byte(body)
}

func postWebhook(t *testing.T, srv *Server, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/evsys", bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Token "+token)
	}
	rr := httptest.NewRecorder()
	srv.handleEVSysWebhook(rr, req)
	return rr
}

func TestChargerWebhookRejectsBadToken(t *testing.T) {
	srv, agent := newChargerTestServer(t, []ChargerLink{testLink()})

	rr := postWebhook(t, srv, "wrong", webhookBody(evsysTransactionStart, "e1", 1, "loc-01"))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if cmds := sentCommands(t, agent); len(cmds) != 0 {
		t.Fatalf("unauthenticated request produced commands: %+v", cmds)
	}
}

// An unconfigured token must not fail open: this endpoint drives the battery and
// is reachable from the public internet.
func TestChargerWebhookDisabledWithoutToken(t *testing.T) {
	dir := chargerTempDir(t)
	srv := New(Config{
		ChargerLinks:    filepath.Join(dir, "charger-links.json"),
		ChargerSessions: filepath.Join(dir, "charger-sessions.json"),
		ConfigStore:     filepath.Join(dir, "agent-configs.json"),
		SessionDB:       filepath.Join(dir, "sessions.db"),
	}, testLogger())

	rr := postWebhook(t, srv, "", webhookBody(evsysTransactionStart, "e1", 1, "loc-01"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestChargerWebhookStartsDischarge(t *testing.T) {
	srv, agent := newChargerTestServer(t, []ChargerLink{testLink()})

	rr := postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStart, "e1", 1, "loc-01"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	cmds := sentCommands(t, agent)
	if len(cmds) != 1 {
		t.Fatalf("got %d commands, want 1: %+v", len(cmds), cmds)
	}
	if cmds[0].Command != chargerCmdStartDischarge {
		t.Fatalf("command = %q, want %q", cmds[0].Command, chargerCmdStartDischarge)
	}
	if cmds[0].Target != "battery1" {
		t.Fatalf("target = %q, want battery1", cmds[0].Target)
	}

	var payload chargerStartPayload
	if err := json.Unmarshal(cmds[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Power != 3000 || payload.PowerLimit != 3000 || payload.SocLimit != 40 {
		t.Fatalf("payload = %+v, want power/limit 3000 soc 40", payload)
	}
	if payload.Source != commandSourceCharger {
		t.Fatalf("source = %q, want %q", payload.Source, commandSourceCharger)
	}
}

// evsys delivers at-least-once, so a redelivered start must not re-command.
func TestChargerWebhookDuplicateStartIsNoop(t *testing.T) {
	srv, agent := newChargerTestServer(t, []ChargerLink{testLink()})

	postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStart, "e1", 1, "loc-01"))
	sentCommands(t, agent) // drain the first start

	rr := postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStart, "e1", 1, "loc-01"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if cmds := sentCommands(t, agent); len(cmds) != 0 {
		t.Fatalf("duplicate start produced commands: %+v", cmds)
	}
	if got := len(srv.chargerSessions.List()); got != 1 {
		t.Fatalf("active sessions = %d, want 1", got)
	}
}

func TestChargerWebhookStopReleasesBattery(t *testing.T) {
	srv, agent := newChargerTestServer(t, []ChargerLink{testLink()})

	postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStart, "e1", 1, "loc-01"))
	sentCommands(t, agent)

	rr := postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStop, "e2", 1, "loc-01"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	cmds := sentCommands(t, agent)
	if len(cmds) != 1 || cmds[0].Command != chargerCmdStopDischarge {
		t.Fatalf("got %+v, want a single stop_discharge", cmds)
	}
	if got := len(srv.chargerSessions.List()); got != 0 {
		t.Fatalf("active sessions = %d, want 0", got)
	}
}

// A stop we never saw a start for (server restarted, event replayed) is ignored
// rather than stopping a discharge that something else owns.
func TestChargerWebhookUnknownStopIsNoop(t *testing.T) {
	srv, agent := newChargerTestServer(t, []ChargerLink{testLink()})

	rr := postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStop, "e2", 99, "loc-01"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if cmds := sentCommands(t, agent); len(cmds) != 0 {
		t.Fatalf("unknown stop produced commands: %+v", cmds)
	}
}

// Two cars charging at one site share a battery: exactly one start when the
// first arrives, and no stop until the last one leaves.
func TestChargerConcurrentSessionsRefcount(t *testing.T) {
	srv, agent := newChargerTestServer(t, []ChargerLink{testLink()})

	postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStart, "e1", 1, "loc-01"))
	postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStart, "e2", 2, "loc-01"))

	cmds := sentCommands(t, agent)
	if len(cmds) != 2 {
		t.Fatalf("got %d commands, want 2 (one per session): %+v", len(cmds), cmds)
	}
	for _, c := range cmds {
		if c.Command != chargerCmdStartDischarge {
			t.Fatalf("command = %q, want start_discharge", c.Command)
		}
	}

	// First car leaves: the second is still charging, so no stop.
	postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStop, "e3", 1, "loc-01"))
	cmds = sentCommands(t, agent)
	if len(cmds) != 1 || cmds[0].Command != chargerCmdStartDischarge {
		t.Fatalf("got %+v, want the discharge re-asserted, not stopped", cmds)
	}

	// Second car leaves: now release the battery.
	postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStop, "e4", 2, "loc-01"))
	cmds = sentCommands(t, agent)
	if len(cmds) != 1 || cmds[0].Command != chargerCmdStopDischarge {
		t.Fatalf("got %+v, want a single stop_discharge", cmds)
	}
}

// OCPP 2.0.1 events carry no location, so the charge point id must resolve.
func TestChargerLinkMatchesByChargePointWithoutLocation(t *testing.T) {
	srv, agent := newChargerTestServer(t, []ChargerLink{testLink()})

	postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStart, "e1", 1, ""))

	cmds := sentCommands(t, agent)
	if len(cmds) != 1 || cmds[0].Command != chargerCmdStartDischarge {
		t.Fatalf("got %+v, want a start_discharge matched by charge point id", cmds)
	}
}

func TestChargerWebhookUnknownLocationIgnored(t *testing.T) {
	srv, agent := newChargerTestServer(t, []ChargerLink{testLink()})

	body := []byte(`{"id":"e1","type":"transaction.start","source":"evsys","sequence":1,
		"data":{"charge_point_id":"Other","connector_id":1,"location_id":"loc-99","transaction_id":7}}`)

	rr := postWebhook(t, srv, testChargerToken, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (acknowledged, not retried)", rr.Code)
	}
	if cmds := sentCommands(t, agent); len(cmds) != 0 {
		t.Fatalf("unlinked location produced commands: %+v", cmds)
	}
}

func TestChargerWebhookIgnoresOtherEventTypes(t *testing.T) {
	srv, agent := newChargerTestServer(t, []ChargerLink{testLink()})

	rr := postWebhook(t, srv, testChargerToken, webhookBody("status", "e1", 1, "loc-01"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if cmds := sentCommands(t, agent); len(cmds) != 0 {
		t.Fatalf("status event produced commands: %+v", cmds)
	}
}

// A transaction.stop lost for good must not leave the battery discharging.
func TestChargerSweeperReleasesExpiredSession(t *testing.T) {
	srv, agent := newChargerTestServer(t, []ChargerLink{testLink()})

	postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStart, "e1", 1, "loc-01"))
	sentCommands(t, agent)

	// The session started at 2026-08-04T10:00Z with a 120 minute cap.
	srv.sweepChargerSessions(time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC))

	cmds := sentCommands(t, agent)
	if len(cmds) != 1 || cmds[0].Command != chargerCmdStopDischarge {
		t.Fatalf("got %+v, want a stop_discharge from the sweeper", cmds)
	}
	if got := len(srv.chargerSessions.List()); got != 0 {
		t.Fatalf("active sessions = %d, want 0", got)
	}
}

func TestChargerSweeperKeepsLiveSession(t *testing.T) {
	srv, agent := newChargerTestServer(t, []ChargerLink{testLink()})

	postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStart, "e1", 1, "loc-01"))
	sentCommands(t, agent)

	srv.sweepChargerSessions(time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC))

	if cmds := sentCommands(t, agent); len(cmds) != 0 {
		t.Fatalf("sweeper touched a live session: %+v", cmds)
	}
	if got := len(srv.chargerSessions.List()); got != 1 {
		t.Fatalf("active sessions = %d, want 1", got)
	}
}

// An agent that restarts loses its in-memory override, so the server must
// re-assert the discharge when it comes back.
func TestChargerSessionsReassertedOnReconnect(t *testing.T) {
	srv, agent := newChargerTestServer(t, []ChargerLink{testLink()})

	postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStart, "e1", 1, "loc-01"))
	sentCommands(t, agent)

	srv.reassertChargerSessions("agent-1")

	cmds := sentCommands(t, agent)
	if len(cmds) != 1 || cmds[0].Command != chargerCmdStartDischarge {
		t.Fatalf("got %+v, want the discharge re-asserted", cmds)
	}
}

// An offline agent must not lose the session: it is re-asserted on reconnect.
func TestChargerSessionSurvivesOfflineAgent(t *testing.T) {
	srv, _ := newChargerTestServer(t, []ChargerLink{testLink()})

	srv.agentsMu.Lock()
	delete(srv.agents, "agent-1")
	srv.agentsMu.Unlock()

	rr := postWebhook(t, srv, testChargerToken, webhookBody(evsysTransactionStart, "e1", 1, "loc-01"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := len(srv.chargerSessions.List()); got != 1 {
		t.Fatalf("active sessions = %d, want 1", got)
	}
}

func TestChargerLinkValidation(t *testing.T) {
	tests := []struct {
		name string
		link ChargerLink
	}{
		{"no name", ChargerLink{LocationId: "l", AgentId: "a", BatteryName: "b", PowerLimit: 1}},
		{"no match key", ChargerLink{Name: "x", AgentId: "a", BatteryName: "b", PowerLimit: 1}},
		{"no agent", ChargerLink{Name: "x", LocationId: "l", BatteryName: "b", PowerLimit: 1}},
		{"no battery", ChargerLink{Name: "x", LocationId: "l", AgentId: "a", PowerLimit: 1}},
		{"zero power", ChargerLink{Name: "x", LocationId: "l", AgentId: "a", BatteryName: "b"}},
		{"soc out of range", ChargerLink{Name: "x", LocationId: "l", AgentId: "a", BatteryName: "b", PowerLimit: 1, SocLimit: 101}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.link.Validate(); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}

	dup := []ChargerLink{testLink(), testLink()}
	if err := ValidateChargerLinks(dup); err == nil {
		t.Fatal("expected duplicate names to be rejected")
	}
}

func TestChargerLinkStorePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "charger-links.json")

	store := NewChargerLinkStore(path)
	if err := store.Set([]ChargerLink{testLink()}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	reloaded := NewChargerLinkStore(path)
	links := reloaded.List()
	if len(links) != 1 || links[0].Name != "site-a" {
		t.Fatalf("reloaded links = %+v, want the stored one", links)
	}
	if _, ok := reloaded.Find("loc-01", ""); !ok {
		t.Fatal("reloaded store did not match by location")
	}
}

func TestChargerLinkDisabledIsNotMatched(t *testing.T) {
	link := testLink()
	link.Enabled = false

	store := NewChargerLinkStore("")
	if err := store.Set([]ChargerLink{link}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, ok := store.Find("loc-01", "Wallbox3"); ok {
		t.Fatal("a disabled link should not match")
	}
}
