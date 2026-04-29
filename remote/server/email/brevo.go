// Package email delivers daily, weekly, and monthly battery activity reports to
// per-agent recipients through the Brevo (https://brevo.com) transactional email API.
//
// The package is consumed by the control server. It does not import the server package
// itself; the server wires it up by passing dependencies (sessiondb store, price fetcher,
// agent listing closure) into NewScheduler.
package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"gok-pi/internal/lib/sl"
)

// brevoEndpoint is the Brevo Transactional Email API endpoint.
const brevoEndpoint = "https://api.brevo.com/v3/smtp/email"

// ProviderConfig holds server-wide Brevo credentials and sender identity.
// The API key is never exposed via the UI; only an enabled flag is reported.
type ProviderConfig struct {
	Enabled     bool   `yaml:"enabled"      env:"GOK_BREVO_ENABLED"      env-default:"false"`
	APIKey      string `yaml:"api_key"      env:"GOK_BREVO_API_KEY"`
	SenderName  string `yaml:"sender_name"  env:"GOK_BREVO_SENDER_NAME"  env-default:"GOK-Pi"`
	SenderEmail string `yaml:"sender_email" env:"GOK_BREVO_SENDER_EMAIL"`
}

// Validate reports whether the provider config is usable.
func (c ProviderConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return errors.New("brevo: api_key is required when enabled")
	}
	if strings.TrimSpace(c.SenderEmail) == "" {
		return errors.New("brevo: sender_email is required when enabled")
	}
	return nil
}

var (
	// ErrBrevoUnauthorized is returned for HTTP 401/403 from Brevo (bad API key).
	ErrBrevoUnauthorized = errors.New("brevo: unauthorized")
	// ErrBrevoRateLimited is returned for HTTP 429 from Brevo.
	ErrBrevoRateLimited = errors.New("brevo: rate limited")
	// ErrBrevoUpstream is returned for HTTP 5xx from Brevo.
	ErrBrevoUpstream = errors.New("brevo: upstream error")
	// ErrBrevoBadRequest is returned for HTTP 4xx (other than 401/403/429).
	ErrBrevoBadRequest = errors.New("brevo: bad request")
)

// BrevoClient sends transactional emails via the Brevo API.
type BrevoClient struct {
	http        *http.Client
	apiKey      string
	senderName  string
	senderEmail string
	log         *slog.Logger
}

// NewBrevo constructs a BrevoClient. Returns nil if cfg.Enabled is false.
func NewBrevo(cfg ProviderConfig, log *slog.Logger) *BrevoClient {
	if !cfg.Enabled {
		return nil
	}
	return &BrevoClient{
		http:        &http.Client{Timeout: 30 * time.Second},
		apiKey:      cfg.APIKey,
		senderName:  cfg.SenderName,
		senderEmail: cfg.SenderEmail,
		log:         log.With(sl.Module("email.brevo")),
	}
}

type brevoSender struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

type brevoRecipient struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type brevoSendRequest struct {
	Sender      brevoSender      `json:"sender"`
	To          []brevoRecipient `json:"to"`
	Subject     string           `json:"subject"`
	HTMLContent string           `json:"htmlContent,omitempty"`
}

type brevoErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Send delivers an HTML email to the given recipients. It performs up to 2 retries
// on transient failures (5xx, network errors) with linear 5 s backoff.
func (c *BrevoClient) Send(ctx context.Context, to []string, subject, html string) error {
	if c == nil {
		return errors.New("brevo: client is nil")
	}
	if len(to) == 0 {
		return errors.New("brevo: no recipients")
	}

	recipients := make([]brevoRecipient, 0, len(to))
	for _, addr := range to {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		recipients = append(recipients, brevoRecipient{Email: addr})
	}
	if len(recipients) == 0 {
		return errors.New("brevo: no valid recipients")
	}

	payload := brevoSendRequest{
		Sender:      brevoSender{Name: c.senderName, Email: c.senderEmail},
		To:          recipients,
		Subject:     subject,
		HTMLContent: html,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal brevo payload: %w", err)
	}

	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := c.do(ctx, body)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryable(err) {
			return err
		}
		if attempt < maxAttempts {
			c.log.Warn("brevo send retry",
				slog.Int("attempt", attempt),
				sl.Err(err),
				sl.Secret("api_key", c.apiKey),
			)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 5 * time.Second):
			}
		}
	}
	return lastErr
}

func (c *BrevoClient) do(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, brevoEndpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create brevo request: %w", err)
	}
	req.Header.Set("api-key", c.apiKey)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("brevo network: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrBrevoUnauthorized, parseBrevoError(respBody))
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", ErrBrevoRateLimited, parseBrevoError(respBody))
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: HTTP %d %s", ErrBrevoUpstream, resp.StatusCode, parseBrevoError(respBody))
	default:
		return fmt.Errorf("%w: HTTP %d %s", ErrBrevoBadRequest, resp.StatusCode, parseBrevoError(respBody))
	}
}

func parseBrevoError(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var e brevoErrorBody
	if err := json.Unmarshal(body, &e); err == nil && e.Message != "" {
		if e.Code != "" {
			return fmt.Sprintf("%s: %s", e.Code, e.Message)
		}
		return e.Message
	}
	return truncate(string(body), 200)
}

func isRetryable(err error) bool {
	return errors.Is(err, ErrBrevoUpstream) || errors.Is(err, ErrBrevoRateLimited) || isNetworkError(err)
}

func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrBrevoUnauthorized) || errors.Is(err, ErrBrevoBadRequest) {
		return false
	}
	// We wrap network errors in `fmt.Errorf("brevo network: %w", err)` in do().
	return strings.Contains(err.Error(), "brevo network:")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
