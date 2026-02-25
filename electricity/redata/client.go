package redata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"time"
)

var (
	ErrNoData   = errors.New("no price data available for requested date")
	ErrAPIError = errors.New("REData API error")
)

const (
	baseURL      = "https://apidatos.ree.es"
	pvpcSeriesID = "1001"
)

type Client struct {
	http *http.Client
	log  *slog.Logger
}

func NewClient(log *slog.Logger) *Client {
	return &Client{
		http: &http.Client{Timeout: 30 * time.Second},
		log:  log,
	}
}

// FetchPrices fetches hourly PVPC prices for the given date.
// Returns ErrNoData if prices are not yet published (e.g., tomorrow's prices before ~20:15 CET).
func (c *Client) FetchPrices(ctx context.Context, date time.Time) ([]HourlyPrice, error) {
	startDate := date.Format("2006-01-02T00:00")
	endDate := date.Format("2006-01-02T23:59")

	url := fmt.Sprintf(
		"%s/en/datos/mercados/precios-mercados-tiempo-real?start_date=%s&end_date=%s&time_trunc=hour&geo_limit=peninsular",
		baseURL, startDate, endDate,
	)

	c.log.Debug("fetching prices from REData", slog.String("url", url), slog.String("date", date.Format("2006-01-02")))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch prices: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusNotFound {
		return nil, ErrNoData
	}

	if resp.StatusCode != http.StatusOK {
		c.log.Warn("REData API non-200 response",
			slog.Int("status", resp.StatusCode),
			slog.String("url", url),
			slog.String("body", truncate(string(body), 500)),
		)

		var apiErr apiError
		if json.Unmarshal(body, &apiErr) == nil && len(apiErr.Errors) > 0 {
			detail := apiErr.Errors[0].Detail
			if detail == "" {
				detail = apiErr.Errors[0].Title
			}
			return nil, fmt.Errorf("%w: %s", ErrAPIError, detail)
		}
		return nil, fmt.Errorf("%w: HTTP %d", ErrAPIError, resp.StatusCode)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return c.extractPVPC(apiResp)
}

func (c *Client) extractPVPC(resp apiResponse) ([]HourlyPrice, error) {
	for _, series := range resp.Included {
		if series.ID != pvpcSeriesID {
			continue
		}

		prices := make([]HourlyPrice, 0, len(series.Attributes.Values))
		for _, v := range series.Attributes.Values {
			dt, err := time.Parse("2006-01-02T15:04:05.000-07:00", v.DateTime)
			if err != nil {
				c.log.Warn("skip price value with unparseable datetime", slog.String("datetime", v.DateTime), slog.Any("error", err))
				continue
			}
			prices = append(prices, HourlyPrice{
				DateTime: dt,
				Hour:     dt.Hour(),
				Price:    v.Value,
			})
		}

		sort.Slice(prices, func(i, j int) bool {
			return prices[i].Hour < prices[j].Hour
		})

		if len(prices) == 0 {
			return nil, ErrNoData
		}

		c.log.Info("fetched PVPC prices",
			slog.Int("count", len(prices)),
			slog.String("date", prices[0].DateTime.Format("2006-01-02")),
		)

		return prices, nil
	}

	return nil, fmt.Errorf("%w: PVPC series (id %s) not found in response", ErrAPIError, pvpcSeriesID)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
