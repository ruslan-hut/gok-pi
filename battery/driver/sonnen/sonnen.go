// Package sonnen implements the battery driver for the Sonnen battery REST API.
//
// Sonnen API endpoints used:
//   - GET  /status                           → battery state (SoC, capacity, power, mode)
//   - POST /setpoint/discharge/{power}       → start/stop discharge
//   - POST /setpoint/charge/{power}          → start/stop charge
//   - PUT  /configurations                   → change operating mode (EM_OperatingMode)
//
// Operating modes: "1" = manual (agent-controlled), "2" = automatic (battery self-managed).
// Auth is via "Auth-Token" header using the token from config.yml.
package sonnen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gok-pi/battery/driver"
	"gok-pi/battery/entity"
	"gok-pi/internal/lib/sl"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

func init() {
	driver.Register("sonnen", New)
}

const (
	maxRetry     = 5   // Maximum number of HTTP request attempts
	retryStep    = 3   // Linear backoff step in seconds: attempt_number * retryStep
	opModeAuto   = "2" // Sonnen operating mode: automatic (battery self-managed)
	opModeManual = "1" // Sonnen operating mode: manual (agent-controlled discharge/charge)
)

var httpClient = &http.Client{}

// httpStatusError carries an HTTP status code so the retry loop can distinguish a
// retryable failure (5xx / 429) from a terminal one (other 4xx, e.g. bad token/URL).
type httpStatusError struct {
	code int
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("received status code: %d", e.code)
}

// retryable reports whether an error from doRequest is worth retrying. Network and
// timeout errors are retryable; HTTP 4xx (except 429 Too Many Requests) are terminal.
func retryable(err error) bool {
	var se *httpStatusError
	if errors.As(err, &se) {
		if se.code == http.StatusTooManyRequests {
			return true
		}
		return se.code < 400 || se.code >= 500
	}
	return true
}

type Driver struct {
	url   string
	token string
	log   *slog.Logger
}

func New(cfg entity.BatteryConfig, log *slog.Logger) (driver.Driver, error) {
	log.With(
		slog.String("url", cfg.Url),
		sl.Secret("token", cfg.Token),
	).Info("creating sonnen api client")
	return &Driver{
		url:   cfg.Url,
		token: cfg.Token,
		log:   log.With(sl.Module("sonnen")),
	}, nil
}

func (c *Driver) Status() (*entity.SystemStatus, error) {
	body, err := c.requestWithRetry(http.MethodGet, nil, c.url, "status")
	if err != nil {
		return nil, err
	}
	status, err := entity.ParseSystemStatus(body)
	if err != nil {
		return nil, fmt.Errorf("parsing status: %w", err)
	}
	return status, nil
}

func (c *Driver) StartDischarge(power int) error {
	_, err := c.requestWithRetry(http.MethodPost, nil, c.url, "setpoint", "discharge", fmt.Sprintf("%d", power))
	return err
}

func (c *Driver) StopDischarge() error {
	_, err := c.requestWithRetry(http.MethodPost, nil, c.url, "setpoint", "discharge", "0")
	return err
}

func (c *Driver) StartCharge(power int) error {
	_, err := c.requestWithRetry(http.MethodPost, nil, c.url, "setpoint", "charge", fmt.Sprintf("%d", power))
	return err
}

func (c *Driver) StopCharge() error {
	_, err := c.requestWithRetry(http.MethodPost, nil, c.url, "setpoint", "charge", "0")
	return err
}

func (c *Driver) SwitchOperatingModeToManual(currentMode string) error {
	if currentMode == opModeManual {
		return nil
	}
	return c.doRequestChangeConfig("EM_OperatingMode", opModeManual)
}

func (c *Driver) SwitchOperatingModeToAuto(currentMode string) error {
	if currentMode == opModeAuto {
		return nil
	}
	return c.doRequestChangeConfig("EM_OperatingMode", opModeAuto)
}

func (c *Driver) fullPath(params ...string) string {
	if len(params) == 0 {
		return ""
	}

	baseURL := params[0]
	if len(params) == 1 {
		return baseURL
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		return strings.Join(params, "/")
	}

	pathSegments := params[1:]
	joinedPath := path.Join(pathSegments...)

	if u.Path == "" {
		u.Path = "/" + joinedPath
	} else {
		basePath := strings.TrimSuffix(u.Path, "/")
		u.Path = basePath + "/" + joinedPath
	}

	return u.String()
}

func (c *Driver) requestWithRetry(method string, data interface{}, params ...string) ([]byte, error) {
	reqPath := c.fullPath(params...)
	log := c.log.With(
		slog.String("url", reqPath),
		slog.String("method", method),
	)
	var body []byte
	if data != nil {
		var err error
		body, err = json.Marshal(data)
		if err != nil {
			log.Error("marshalling body", sl.Err(err))
			return nil, fmt.Errorf("marshalling body: %w", err)
		}
	}

	var lastErr error
	for i := 0; i < maxRetry; i++ {
		responseBody, err := c.doRequest(method, reqPath, bytes.NewReader(body))
		if err == nil {
			return responseBody, nil
		}
		lastErr = err
		if !retryable(err) {
			log.With(sl.Err(err)).Debug("not retrying non-retryable request")
			return nil, err
		}
		// Don't sleep after the final attempt.
		if i < maxRetry-1 {
			log.With(slog.Int("attempt", i+1)).Debug("retrying request")
			time.Sleep(time.Duration((i+1)*retryStep) * time.Second)
		}
	}
	return nil, fmt.Errorf("request failed after %d retries: %w", maxRetry, lastErr)
}

func (c *Driver) doRequest(method, url string, reader io.Reader) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var err error
	log := c.log.With(
		slog.String("url", url),
		sl.Secret("token", c.token),
		slog.String("method", method),
	)
	t1 := time.Now()
	defer func() {
		log = log.With(slog.Float64("duration", time.Since(t1).Seconds()))
		if err != nil {
			log.Error("api request", sl.Err(err))
		} else {
			log.Debug("api request")
		}
	}()

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Auth-Token", c.token)

	resp, err := httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("request timeout")
		}
		return nil, err
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	log = log.With(slog.Int("status", resp.StatusCode))
	if resp.StatusCode >= 400 {
		err = &httpStatusError{code: resp.StatusCode}
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (c *Driver) doRequestChangeConfig(parameter, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reqPath := c.fullPath(c.url, "configurations")
	reader := strings.NewReader(fmt.Sprintf(`%s=%s`, parameter, value))

	var err error
	log := c.log.With(
		slog.String("url", reqPath),
		sl.Secret("token", c.token),
		slog.String("method", "PUT"),
		slog.String(parameter, value),
	)
	t1 := time.Now()
	defer func() {
		log = log.With(slog.Float64("duration", time.Since(t1).Seconds()))
		if err != nil {
			log.Error("change config request", sl.Err(err))
		} else {
			log.Debug("change config request")
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqPath, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Auth-Token", c.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("request timeout")
		}
		return err
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	log = log.With(slog.Int("status", resp.StatusCode))
	if resp.StatusCode >= 400 {
		err = fmt.Errorf("received status code: %d", resp.StatusCode)
		return err
	}
	return nil
}
