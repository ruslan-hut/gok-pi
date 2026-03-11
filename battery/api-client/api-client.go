package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

const (
	maxRetry     = 5
	retryStep    = 3
	opModeAuto   = "2"
	opModeManual = "1"
)

var httpClient = &http.Client{}

type ApiClient struct {
	url   string
	token string
	log   *slog.Logger
}

func New(url, token string, log *slog.Logger) *ApiClient {
	log.With(
		slog.String("url", url),
		sl.Secret("token", token),
	).Info("creating api client")
	return &ApiClient{
		url:   url,
		token: token,
		log:   log.With(sl.Module("client")),
	}
}

func (c *ApiClient) Status() (*entity.SystemStatus, error) {
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

func (c *ApiClient) StartDischarge(power int) error {
	_, err := c.requestWithRetry(http.MethodPost, nil, c.url, "setpoint", "discharge", fmt.Sprintf("%d", power))
	return err
}

func (c *ApiClient) StopDischarge() error {
	_, err := c.requestWithRetry(http.MethodPost, nil, c.url, "setpoint", "discharge", "0")
	return err
}

func (c *ApiClient) StartCharge(power int) error {
	_, err := c.requestWithRetry(http.MethodPost, nil, c.url, "setpoint", "charge", fmt.Sprintf("%d", power))
	return err
}

func (c *ApiClient) StopCharge() error {
	_, err := c.requestWithRetry(http.MethodPost, nil, c.url, "setpoint", "charge", "0")
	return err
}

// SwitchOperatingModeToManual switches the operating mode of the API client to manual.
// It returns nil if the current mode is already set to manual, otherwise it sends a request to change the operating mode to manual.
func (c *ApiClient) SwitchOperatingModeToManual(currentMode string) error {
	if currentMode == opModeManual {
		return nil
	}
	return c.doRequestChangeConfig("EM_OperatingMode", opModeManual)
}

// SwitchOperatingModeToAuto switches the operating mode of the API client to automatic.
// It sends a request to change the operating mode to automatic.
func (c *ApiClient) SwitchOperatingModeToAuto(currentMode string) error {
	if currentMode == opModeAuto {
		return nil
	}
	return c.doRequestChangeConfig("EM_OperatingMode", opModeAuto)
}

// fullPath constructs a full URL by joining the base URL with path segments.
// It properly handles URL path joining to avoid issues with double slashes or missing slashes.
func (c *ApiClient) fullPath(params ...string) string {
	if len(params) == 0 {
		return ""
	}

	baseURL := params[0]
	if len(params) == 1 {
		return baseURL
	}

	// Parse the base URL to ensure proper joining
	u, err := url.Parse(baseURL)
	if err != nil {
		// If parsing fails, fall back to simple string joining
		return strings.Join(params, "/")
	}

	// Join path segments properly using path.Join, then append to base path
	pathSegments := params[1:]
	joinedPath := path.Join(pathSegments...)

	// Append to existing path, ensuring proper slash handling
	if u.Path == "" {
		u.Path = "/" + joinedPath
	} else {
		// Remove trailing slash from base path if present, then add joined path
		basePath := strings.TrimSuffix(u.Path, "/")
		u.Path = basePath + "/" + joinedPath
	}

	return u.String()
}

// requestWithRetry sends an HTTP request with retry logic.
// It takes in the HTTP method, request body data, and optional parameters strings.
// It returns the response body if successful or an error if the request failed after the maximum number of retries.
// If the request body data is not nil, it converts the data into JSON format.
// If marshalling the data fails, it returns an error.
// It retries the request up to a maximum number of times, with a delay between each retry.
// The maximum number of retries and the delay between retries are defined by constants.
// After the maximum number of retries, it returns an error indicating the request failure.
func (c *ApiClient) requestWithRetry(method string, data interface{}, params ...string) ([]byte, error) {
	path := c.fullPath(params...)
	log := c.log.With(
		slog.String("url", path),
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

	for i := 0; i < maxRetry; i++ {
		responseBody, err := c.doRequest(method, path, bytes.NewReader(body))
		if err == nil {
			return responseBody, nil
		}
		log.With(
			slog.Int("attempt", i+1),
		).Debug("retrying request")
		time.Sleep(time.Duration((i+1)*retryStep) * time.Second)
	}
	return nil, fmt.Errorf("request failed after %d retries", maxRetry)
}

func (c *ApiClient) doRequest(method, url string, reader io.Reader) ([]byte, error) {
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
		err = fmt.Errorf("received status code: %d", resp.StatusCode)
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// doRequestChangeConfig sends a request to change the configuration of the API client.
// It takes in a parameter name and its corresponding value as input strings.
// It returns nil if the request is successful, otherwise it returns an error.
// The request is sent as a PUT method to the "configurations" endpoint with the specified parameter and value in the request body.
// The request is made with a timeout of 5 seconds.
// If the request fails with a status code of 400 or higher, an error is returned.
func (c *ApiClient) doRequestChangeConfig(parameter, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := c.fullPath(c.url, "configurations")
	reader := strings.NewReader(fmt.Sprintf(`%s=%s`, parameter, value))

	var err error
	log := c.log.With(
		slog.String("url", url),
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, reader)
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
