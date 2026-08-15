package entity

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Stored configs predate per-recipient subscriptions, so a plain string list must
// keep delivering the same reports to the same people — and must not silently
// enrol them in alerts nobody asked for.
func TestEmailReportsConfigMigratesStringRecipients(t *testing.T) {
	raw := []byte(`{
		"enabled": true,
		"recipients": ["a@example.com", " b@example.com ", ""],
		"daily": true,
		"weekly": true,
		"monthly": false,
		"send_hour": 7
	}`)

	var cfg EmailReportsConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := []EmailRecipient{
		{Address: "a@example.com", Daily: true, Weekly: true},
		{Address: "b@example.com", Daily: true, Weekly: true},
	}
	if !reflect.DeepEqual(cfg.Recipients, want) {
		t.Fatalf("migrated recipients = %+v, want %+v", cfg.Recipients, want)
	}
	if got := cfg.RecipientsFor(EmailKindAlerts); len(got) != 0 {
		t.Fatalf("migration must not subscribe anyone to alerts, got %v", got)
	}
	if got := cfg.RecipientsFor(EmailKindMonthly); len(got) != 0 {
		t.Fatalf("monthly was off, got %v", got)
	}
	if cfg.SendHour != 7 || !cfg.Enabled {
		t.Fatalf("scalar fields lost: %+v", cfg)
	}
}

func TestEmailReportsConfigRecipientObjects(t *testing.T) {
	raw := []byte(`{
		"enabled": true,
		"recipients": [
			{"address": "ops@example.com", "daily": false, "alerts": true},
			{"address": "customer@example.com", "monthly": true}
		],
		"daily": true,
		"monthly": true
	}`)

	var cfg EmailReportsConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got := cfg.RecipientsFor(EmailKindAlerts); !reflect.DeepEqual(got, []string{"ops@example.com"}) {
		t.Fatalf("alerts = %v", got)
	}
	if got := cfg.RecipientsFor(EmailKindMonthly); !reflect.DeepEqual(got, []string{"customer@example.com"}) {
		t.Fatalf("monthly = %v", got)
	}
	if got := cfg.RecipientsFor(EmailKindDaily); len(got) != 0 {
		t.Fatalf("nobody subscribed to daily, got %v", got)
	}
}

func TestEmailReportsConfigRoundTrips(t *testing.T) {
	in := EmailReportsConfig{
		Enabled:    true,
		Daily:      true,
		SendHour:   7,
		Recipients: []EmailRecipient{{Address: "ops@example.com", Daily: true, Alerts: true}},
	}

	encoded, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out EmailReportsConfig
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip changed the config: %+v -> %+v", in, out)
	}
}

func TestCloneEmailReportsIsDeep(t *testing.T) {
	in := &EmailReportsConfig{Recipients: []EmailRecipient{{Address: "a@example.com", Daily: true}}}
	out := CloneEmailReports(in)
	out.Recipients[0].Address = "b@example.com"

	if in.Recipients[0].Address != "a@example.com" {
		t.Fatal("clone shares the recipient slice with the original")
	}
}
