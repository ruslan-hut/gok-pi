package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gok-pi/battery/entity"
)

const secretToken = "sonnen-secret-token-123"

// newSecuredServer has login configured, one agent config with a real battery
// token, and returns a valid session token.
func newSecuredServer(t *testing.T) (*Server, string, AgentConfig) {
	t.Helper()
	t.Chdir(t.TempDir()) // New and the stores use relative data/ paths
	srv := New(Config{UIUsername: "u", UIPassword: "p"}, testLogger())
	cfg, err := srv.configs.Save("agent-1", AgentConfigRequest{
		Batteries: batteries(secretToken),
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	session, _, err := srv.auth.login("u", "p")
	if err != nil || session == "" {
		t.Fatalf("login: %v", err)
	}
	return srv, session, cfg
}

func batteries(token string) []entity.BatteryConfig {
	return []entity.BatteryConfig{{Name: "bat-1", Driver: "sonnen", Url: "http://b", Token: token, Enabled: true, CapacityLimit: 500}}
}

func putConfig(t *testing.T, srv *Server, session string, revision int, token string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"revision":  revision,
		"batteries": batteries(token),
		"schedules": []any{},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/agents/agent-1/config", bytes.NewReader(body))
	req.Header.Set(headerAuthToken, session)
	rr := httptest.NewRecorder()
	srv.requireAuthForWrites(srv.handleAgentRoutes)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rr.Code, rr.Body.String())
	}
	return rr
}

func TestConfigGetNeverContainsToken(t *testing.T) {
	srv, session, _ := newSecuredServer(t)
	for name, header := range map[string]string{"anonymous": "", "logged in": session} {
		req := httptest.NewRequest(http.MethodGet, "/api/agents/agent-1/config", nil)
		if header != "" {
			req.Header.Set(headerAuthToken, header)
		}
		rr := httptest.NewRecorder()
		srv.requireAuthForWrites(srv.handleAgentRoutes)(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d", name, rr.Code)
		}
		if strings.Contains(rr.Body.String(), secretToken) {
			t.Fatalf("%s: response contains the token: %s", name, rr.Body.String())
		}
		var got AgentConfig
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Batteries[0].Token != TokenMask {
			t.Fatalf("%s: token = %q, want the mask", name, got.Batteries[0].Token)
		}
	}
}

func TestConfigUpdatedOnUISocketHasNoToken(t *testing.T) {
	srv, _, cfg := newSecuredServer(t)
	ui := &uiConnection{send: make(chan interface{}, 4), done: make(chan struct{})}
	srv.uiClient[ui] = struct{}{}

	srv.broadcastConfigUpdated("agent-1", cfg)

	select {
	case msg := <-ui.send:
		b, ok := msg.([]byte)
		if !ok {
			t.Fatalf("unexpected message type %T", msg)
		}
		if !strings.Contains(string(b), "config.updated") {
			t.Fatalf("not a config.updated message: %s", b)
		}
		if strings.Contains(string(b), secretToken) {
			t.Fatalf("UI socket message contains the token: %s", b)
		}
	case <-time.After(time.Second):
		t.Fatal("no message broadcast")
	}
}

func TestPutWithMaskKeepsToken(t *testing.T) {
	for _, sent := range []string{TokenMask, ""} {
		srv, session, cfg := newSecuredServer(t)
		rr := putConfig(t, srv, session, cfg.Revision, sent)
		if strings.Contains(rr.Body.String(), secretToken) {
			t.Fatalf("PUT response leaks the token")
		}
		stored, _ := srv.configs.Get("agent-1")
		if got := stored.Batteries[0].Token; got != secretToken {
			t.Fatalf("sent %q: stored token = %q, want the original", sent, got)
		}
	}
}

func TestPutWithNewTokenReplacesIt(t *testing.T) {
	srv, session, cfg := newSecuredServer(t)
	putConfig(t, srv, session, cfg.Revision, "brand-new")
	stored, _ := srv.configs.Get("agent-1")
	if got := stored.Batteries[0].Token; got != "brand-new" {
		t.Fatalf("stored token = %q, want brand-new", got)
	}
}

// A mask for a battery that has no stored token (renamed or new) is not saved as one.
func TestMaskWithoutStoredBatteryIsNotSaved(t *testing.T) {
	srv, session, cfg := newSecuredServer(t)
	body, _ := json.Marshal(map[string]any{
		"revision":  cfg.Revision,
		"batteries": []entity.BatteryConfig{{Name: "other", Driver: "sonnen", Url: "http://b", Token: TokenMask, Enabled: true}},
		"schedules": []any{},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/agents/agent-1/config", bytes.NewReader(body))
	req.Header.Set(headerAuthToken, session)
	rr := httptest.NewRecorder()
	srv.requireAuthForWrites(srv.handleAgentRoutes)(rr, req)
	stored, _ := srv.configs.Get("agent-1")
	if stored.Batteries[0].Token != "" {
		t.Fatalf("mask saved as token: %q", stored.Batteries[0].Token)
	}
}

// The agent channel is the one place the real token must still travel.
func TestConfigPushedToAgentCarriesRealToken(t *testing.T) {
	srv, session, cfg := newSecuredServer(t)
	agent := &agentConnection{id: "agent-1", send: make(chan interface{}, 4), done: make(chan struct{})}
	srv.agents["agent-1"] = agent

	putConfig(t, srv, session, cfg.Revision, TokenMask)

	select {
	case msg := <-agent.send:
		push, ok := msg.(ConfigPush)
		if !ok {
			t.Fatalf("unexpected message %T", msg)
		}
		if got := push.Config.Batteries[0].Token; got != secretToken {
			t.Fatalf("agent got token %q, want the real one", got)
		}
	case <-time.After(time.Second):
		t.Fatal("nothing pushed to the agent")
	}
}

func TestAgentLogsAndDiagNeedLogin(t *testing.T) {
	srv, session, _ := newSecuredServer(t)
	for _, path := range []string{"/api/agents/agent-1/logs", "/api/agents/agent-1/diag"} {
		rr := httptest.NewRecorder()
		srv.handleAgentRoutes(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s anonymous: status %d, want 401", path, rr.Code)
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set(headerAuthToken, session)
		rr = httptest.NewRecorder()
		srv.handleAgentRoutes(rr, req)
		if rr.Code == http.StatusUnauthorized {
			t.Fatalf("%s logged in: got 401", path)
		}
	}
}

func TestChargerSessionsHideWhoIsCharging(t *testing.T) {
	srv, session, _ := newSecuredServer(t)
	if _, err := srv.chargerSessions.Add(ChargerSession{Key: "k", IdTag: "RFID-1", Username: "Alice"}); err != nil {
		t.Fatalf("add session: %v", err)
	}

	rr := httptest.NewRecorder()
	srv.handleChargerSessions(rr, httptest.NewRequest(http.MethodGet, "/api/charger-sessions", nil))
	if strings.Contains(rr.Body.String(), "RFID-1") || strings.Contains(rr.Body.String(), "Alice") {
		t.Fatalf("anonymous reader sees who is charging: %s", rr.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/charger-sessions", nil)
	req.Header.Set(headerAuthToken, session)
	rr = httptest.NewRecorder()
	srv.handleChargerSessions(rr, req)
	if !strings.Contains(rr.Body.String(), "RFID-1") {
		t.Fatalf("logged-in reader lost the details: %s", rr.Body.String())
	}
}
