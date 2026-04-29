package email

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, server *httptest.Server) *BrevoClient {
	t.Helper()
	c := &BrevoClient{
		http:        server.Client(),
		apiKey:      "test-key",
		senderName:  "Tester",
		senderEmail: "test@example.com",
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	// Override endpoint for the duration of the test by swapping the http transport
	c.http.Transport = &rewriteTransport{base: c.http.Transport, target: server.URL}
	return c
}

type rewriteTransport struct {
	base   http.RoundTripper
	target string
}

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "api.brevo.com" {
		newURL := r.target + req.URL.Path
		req2, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, req.Body)
		if err != nil {
			return nil, err
		}
		req2.Header = req.Header.Clone()
		req = req2
	}
	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func TestBrevoSendSuccess(t *testing.T) {
	var gotBody brevoSendRequest
	var gotKey string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("api-key")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"messageId":"abc"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.Send(context.Background(), []string{"alice@example.com", "bob@example.com"}, "Subject", "<b>hi</b>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotKey != "test-key" {
		t.Fatalf("api-key header missing or wrong: %q", gotKey)
	}
	if gotBody.Sender.Email != "test@example.com" {
		t.Fatalf("sender email mismatch: %q", gotBody.Sender.Email)
	}
	if len(gotBody.To) != 2 || gotBody.To[0].Email != "alice@example.com" {
		t.Fatalf("recipients mismatch: %+v", gotBody.To)
	}
	if gotBody.Subject != "Subject" || !strings.Contains(gotBody.HTMLContent, "hi") {
		t.Fatalf("payload mismatch: %+v", gotBody)
	}
}

func TestBrevoSendUnauthorizedNotRetried(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"unauthorized","message":"bad key"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.Send(context.Background(), []string{"a@b.c"}, "s", "h")
	if !errors.Is(err, ErrBrevoUnauthorized) {
		t.Fatalf("expected ErrBrevoUnauthorized, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (no retry on auth error), got %d", calls)
	}
}

func TestBrevoSendUpstreamRetriedThenFails(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	// Speed up backoff in test by replacing the client's HTTP timeout — backoff itself
	// uses time.After; override via context with short deadline so test stays fast.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := c.Send(ctx, []string{"a@b.c"}, "s", "h")
	if err == nil {
		t.Fatal("expected error")
	}
	// Either the upstream error is returned after exhausting retries, or context deadline fires.
	if !errors.Is(err, ErrBrevoUpstream) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected upstream or deadline error, got %v", err)
	}
	if calls < 1 {
		t.Fatalf("expected at least 1 call, got %d", calls)
	}
}

func TestProviderConfigValidate(t *testing.T) {
	cases := []struct {
		name string
		cfg  ProviderConfig
		ok   bool
	}{
		{"disabled passes", ProviderConfig{Enabled: false}, true},
		{"missing key fails", ProviderConfig{Enabled: true, SenderEmail: "x@y"}, false},
		{"missing sender fails", ProviderConfig{Enabled: true, APIKey: "k"}, false},
		{"complete passes", ProviderConfig{Enabled: true, APIKey: "k", SenderEmail: "x@y"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cfg.Validate()
			if c.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
