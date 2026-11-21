package redata

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
)

func pvpcValues(n int) []apiPriceValue {
	vals := make([]apiPriceValue, n)
	// Reverse order to check sorting.
	for i := range n {
		h := n - 1 - i
		vals[i] = apiPriceValue{
			Value:    float64(100 + h),
			DateTime: fmt.Sprintf("2026-11-22T%02d:00:00.000+01:00", h),
		}
	}
	return vals
}

func TestExtractPVPC(t *testing.T) {
	spot := apiSeries{ID: "600", Attributes: apiSeriesAttrs{Values: pvpcValues(24)}}

	tests := []struct {
		name      string
		resp      apiResponse
		wantErr   error
		wantCount int
	}{
		{
			name:    "only spot series",
			resp:    apiResponse{Included: []apiSeries{spot}},
			wantErr: ErrNoData,
		},
		{
			name:    "pvpc series empty",
			resp:    apiResponse{Included: []apiSeries{spot, {ID: pvpcSeriesID}}},
			wantErr: ErrNoData,
		},
		{
			name: "pvpc series full day",
			resp: apiResponse{Included: []apiSeries{
				spot,
				{ID: pvpcSeriesID, Attributes: apiSeriesAttrs{Values: pvpcValues(24)}},
			}},
			wantCount: 24,
		},
	}

	c := NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prices, err := c.extractPVPC(tt.resp)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if errors.Is(err, ErrAPIError) {
					t.Fatalf("err = %v, must not be ErrAPIError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(prices) != tt.wantCount {
				t.Fatalf("len(prices) = %d, want %d", len(prices), tt.wantCount)
			}
			for i, p := range prices {
				if p.Hour != i {
					t.Errorf("prices[%d].Hour = %d, want %d", i, p.Hour, i)
				}
				if p.Price != float64(100+i) {
					t.Errorf("prices[%d].Price = %v, want %v", i, p.Price, float64(100+i))
				}
			}
		})
	}
}
