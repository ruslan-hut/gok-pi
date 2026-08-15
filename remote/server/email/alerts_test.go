package email

import (
	"strings"
	"testing"
	"time"
)

func TestRenderConnectivityAlertOffline(t *testing.T) {
	since := time.Date(2026, 8, 14, 14, 35, 0, 0, time.UTC)
	subject, html, err := RenderConnectivityAlert(ConnectivityAlert{
		Kind:       AlertOffline,
		AgentID:    "fa5af85824c2464a",
		DeviceName: "torrepi",
		Timezone:   "Europe/Madrid",
		Since:      since,
		Now:        since.Add(13*time.Hour + 25*time.Minute),
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if !strings.Contains(subject, "torrepi") || !strings.Contains(subject, "offline") {
		t.Fatalf("subject should name the device and the state, got %q", subject)
	}
	for _, want := range []string{"fa5af85824c2464a", "13h 25m", "torrepi"} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected %q in the body", want)
		}
	}
}

func TestRenderConnectivityAlertFallsBackToAgentID(t *testing.T) {
	subject, _, err := RenderConnectivityAlert(ConnectivityAlert{
		Kind:    AlertRecovered,
		AgentID: "agent-1",
		Now:     time.Now(),
		Since:   time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(subject, "agent-1") || !strings.Contains(subject, "back online") {
		t.Fatalf("unexpected subject %q", subject)
	}
}

func TestFormatOutage(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "less than a minute"},
		{9 * time.Minute, "9m"},
		{2 * time.Hour, "2h"},
		{13*time.Hour + 25*time.Minute, "13h 25m"},
	}
	for _, c := range cases {
		if got := formatOutage(c.in); got != c.want {
			t.Fatalf("formatOutage(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}
