package redata

import "time"

// HourlyPrice represents a single hourly electricity price from the REData API.
type HourlyPrice struct {
	DateTime time.Time `json:"datetime"`
	Hour     int       `json:"hour"`
	Price    float64   `json:"price_eur_mwh"`
}

// apiResponse represents the top-level REData API response.
type apiResponse struct {
	Data     apiData     `json:"data"`
	Included []apiSeries `json:"included"`
}

type apiData struct {
	Type       string        `json:"type"`
	ID         string        `json:"id"`
	Attributes apiAttributes `json:"attributes"`
}

type apiAttributes struct {
	Title      string `json:"title"`
	LastUpdate string `json:"last-update"`
}

type apiSeries struct {
	Type       string         `json:"type"`
	ID         string         `json:"id"`
	Attributes apiSeriesAttrs `json:"attributes"`
}

type apiSeriesAttrs struct {
	Title      string          `json:"title"`
	LastUpdate string          `json:"last-update"`
	Values     []apiPriceValue `json:"values"`
}

type apiPriceValue struct {
	Value      float64 `json:"value"`
	Percentage float64 `json:"percentage"`
	DateTime   string  `json:"datetime"`
}

type apiError struct {
	Errors []apiErrorDetail `json:"errors"`
}

type apiErrorDetail struct {
	Code   int    `json:"code"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}
