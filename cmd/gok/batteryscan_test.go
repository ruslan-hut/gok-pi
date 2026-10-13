package main

import (
	"context"
	"gok-pi/battery/entity"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestParseBatteryURL(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		scheme string
		host   string
		port   int
		path   string
	}{
		{"http implicit port", "http://192.168.68.107/api/v2", "http", "192.168.68.107", 80, "/api/v2"},
		{"https implicit port", "https://192.168.68.107/api/v2", "https", "192.168.68.107", 443, "/api/v2"},
		{"explicit port", "http://192.168.68.107:8080/api/v2", "http", "192.168.68.107", 8080, "/api/v2"},
		{"bare host", "192.168.68.107", "http", "192.168.68.107", 80, ""},
		{"bare host and port", "192.168.68.107:502", "http", "192.168.68.107", 502, ""},
		{"hostname", "http://battery.local/api/v2", "http", "battery.local", 80, "/api/v2"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBatteryURL(tc.raw)
			if err != nil {
				t.Fatalf("parseBatteryURL(%q): %v", tc.raw, err)
			}
			if got.scheme != tc.scheme || got.host != tc.host || got.port != tc.port || got.path != tc.path {
				t.Errorf("got %+v, want scheme=%s host=%s port=%d path=%s",
					got, tc.scheme, tc.host, tc.port, tc.path)
			}
		})
	}
}

func TestParseBatteryURLRejectsEmpty(t *testing.T) {
	if _, err := parseBatteryURL("   "); err == nil {
		t.Error("parseBatteryURL accepted an empty URL")
	}
}

func TestBatteryTargetURLFor(t *testing.T) {
	target, err := parseBatteryURL("http://192.168.68.107/api/v2")
	if err != nil {
		t.Fatalf("parseBatteryURL: %v", err)
	}

	// The API path must survive the host swap: probing the root of a candidate
	// address asks a different question than probing the battery's API.
	if got := target.urlFor("192.168.68.142", 80); got != "http://192.168.68.142/api/v2" {
		t.Errorf("urlFor = %s, want http://192.168.68.142/api/v2", got)
	}
	// A non-default port has to appear, or the probe silently goes to port 80.
	if got := target.urlFor("192.168.68.142", 8080); got != "http://192.168.68.142:8080/api/v2" {
		t.Errorf("urlFor = %s, want the port included", got)
	}

	secure, err := parseBatteryURL("https://192.168.68.107/api/v2")
	if err != nil {
		t.Fatalf("parseBatteryURL: %v", err)
	}
	if got := secure.urlFor("192.168.68.142", 443); got != "https://192.168.68.142/api/v2" {
		t.Errorf("urlFor = %s, want the default https port left off", got)
	}
}

func TestResolveScanBattery(t *testing.T) {
	one := []entity.BatteryConfig{{Name: "BAT-001", Url: "http://192.168.68.107/api/v2"}}
	two := append([]entity.BatteryConfig{}, one...)
	two = append(two, entity.BatteryConfig{Name: "BAT-002"})

	t.Run("single battery needs no name", func(t *testing.T) {
		got, err := resolveScanBattery(one, "")
		if err != nil {
			t.Fatalf("resolveScanBattery: %v", err)
		}
		if got.Name != "BAT-001" {
			t.Errorf("got %q", got.Name)
		}
	})

	t.Run("name is matched case-insensitively", func(t *testing.T) {
		got, err := resolveScanBattery(two, "bat-002")
		if err != nil {
			t.Fatalf("resolveScanBattery: %v", err)
		}
		if got.Name != "BAT-002" {
			t.Errorf("got %q", got.Name)
		}
	})

	t.Run("several batteries need a name", func(t *testing.T) {
		_, err := resolveScanBattery(two, "")
		if err == nil {
			t.Fatal("expected an error when the battery is ambiguous")
		}
		// The error has to name the candidates, or the operator cannot act on it.
		if !strings.Contains(err.Error(), "BAT-001") || !strings.Contains(err.Error(), "BAT-002") {
			t.Errorf("error %q does not list the configured batteries", err)
		}
	})

	t.Run("unknown name", func(t *testing.T) {
		if _, err := resolveScanBattery(two, "BAT-009"); err == nil {
			t.Error("expected an error for an unconfigured battery")
		}
	})

	t.Run("no batteries", func(t *testing.T) {
		if _, err := resolveScanBattery(nil, ""); err == nil {
			t.Error("expected an error when nothing is configured")
		}
	})

	t.Run("a disabled battery is still findable", func(t *testing.T) {
		// Switching a battery off in the config is a common reaction to it going
		// silent, and that is exactly the battery a scan is looking for.
		disabled := []entity.BatteryConfig{{Name: "BAT-001", Enabled: false}}
		if _, err := resolveScanBattery(disabled, "BAT-001"); err != nil {
			t.Errorf("resolveScanBattery skipped a disabled battery: %v", err)
		}
	})
}

func TestDescribeBattery(t *testing.T) {
	got := describeBattery("sonnen", &entity.SystemStatus{
		USOC:                61,
		RSOC:                59,
		RemainingCapacityWh: 10240,
		OperatingMode:       "2",
		SystemStatus:        "OnGrid",
	})

	for _, want := range []string{"sonnen", "61%", "59%", "10240 Wh", "mode 2", "OnGrid"} {
		if !strings.Contains(got, want) {
			t.Errorf("describeBattery = %q, missing %q", got, want)
		}
	}
}

func TestDescribeBatteryOmitsEmptyFields(t *testing.T) {
	got := describeBattery("sonnen", &entity.SystemStatus{USOC: 0, RSOC: 0})
	if strings.Contains(got, "Wh") || strings.Contains(got, "mode") {
		t.Errorf("describeBattery = %q, want the unset fields left out", got)
	}
}

func TestBatteryProbeIdentifiesBattery(t *testing.T) {
	const token = "probe-token"

	var gotToken, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("Auth-Token")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"USOC":61,"RSOC":59,"OperatingMode":"2","RemainingCapacity_Wh":10240}`))
	}))
	defer srv.Close()

	// The probe rebuilds the configured URL against the candidate address, so
	// point the config at a dead address and let the probe aim at the test server.
	battery := entity.BatteryConfig{
		Name:   "BAT-001",
		Driver: "sonnen",
		Url:    "http://192.168.68.107/api/v2",
		Token:  token,
	}
	target, err := parseBatteryURL(battery.Url)
	if err != nil {
		t.Fatalf("parseBatteryURL: %v", err)
	}

	host, port := serverHostPort(t, srv.URL)
	probe := batteryProbe(battery, target, port, discardLogger())

	detail, err := probe(context.Background(), host)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !strings.Contains(detail, "61%") {
		t.Errorf("detail = %q, want the state of charge", detail)
	}
	if gotToken != token {
		t.Errorf("probe sent token %q, want %q", gotToken, token)
	}
	if gotPath != "/api/v2/status" {
		t.Errorf("probe asked for %q, want the configured API path", gotPath)
	}
}

func TestBatteryProbeRejectsNonBattery(t *testing.T) {
	// What the squatter on a reused DHCP lease looks like: it answers TCP and
	// HTTP, but not as the battery. A 404 is terminal in the driver, so this
	// also pins that the probe fails fast instead of retrying for a minute.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	battery := entity.BatteryConfig{Name: "BAT-001", Driver: "sonnen", Url: "http://192.168.68.107/api/v2"}
	target, err := parseBatteryURL(battery.Url)
	if err != nil {
		t.Fatalf("parseBatteryURL: %v", err)
	}

	host, port := serverHostPort(t, srv.URL)
	_, err = batteryProbe(battery, target, port, discardLogger())(context.Background(), host)
	if err == nil {
		t.Fatal("probe accepted a host that is not a battery")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %q, want it to name the status code", err)
	}
}

// serverHostPort splits an httptest server URL into the pieces the probe takes.
func serverHostPort(t *testing.T, raw string) (string, int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("test server port %q: %v", u.Port(), err)
	}
	return u.Hostname(), port
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
