package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/suwandre/arbiter/internal/models"
)

type MexcAdapter struct {
	apiKey     string
	httpClient *http.Client
}

type mexcTicker struct {
	Bid1        float64 `json:"bid1"`
	Ask1        float64 `json:"ask1"`
	FundingRate float64 `json:"fundingRate"`
	Timestamp   int64   `json:"timestamp"`
}

func NewMexcAdapter(apiKey string) *MexcAdapter {
	return &MexcAdapter{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (m *MexcAdapter) Name() string {
	return "mexc"
}

func (m *MexcAdapter) GetFundingRate(ctx context.Context, pair string) (*models.FundingRate, error) {
	ticker, err := m.fetchTicker(ctx, pair)
	if err != nil {
		return nil, err
	}

	// MEXC ticker timestamp is the current time, not next funding time.
	// Use it as a best-effort approximation since the ticker doesn't expose nextSettleTime.
	return &models.FundingRate{
		Exchange:    "mexc",
		Pair:        pair,
		Rate:        ticker.FundingRate,
		NextFunding: time.UnixMilli(ticker.Timestamp),
	}, nil
}

func (m *MexcAdapter) GetSpread(ctx context.Context, pair string) (*models.Spread, error) {
	ticker, err := m.fetchTicker(ctx, pair)
	if err != nil {
		return nil, err
	}

	return &models.Spread{
		Exchange: "mexc",
		Pair:     pair,
		Bid:      ticker.Bid1,
		Ask:      ticker.Ask1,
		Spread:   ticker.Ask1 - ticker.Bid1,
	}, nil
}

func (m *MexcAdapter) GetOrderBookDepth(ctx context.Context, pair string) (*models.OrderBookDepth, error) {
	url := fmt.Sprintf(
		"https://contract.mexc.com/api/v1/contract/depth/%s?limit=5",
		toMexcSymbol(pair),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("mexc depth: failed to build request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mexc depth request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mexc depth: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mexc depth: failed to read response body: %w", err)
	}

	// MEXC depth entries are [price, contractCount, orderCount]
	var raw struct {
		Success bool `json:"success"`
		Code    int  `json:"code"`
		Data    struct {
			Asks [][]float64 `json:"asks"`
			Bids [][]float64 `json:"bids"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("mexc depth: failed to parse response: %w", err)
	}

	if !raw.Success || raw.Code != 0 {
		return nil, fmt.Errorf("mexc API error code %d", raw.Code)
	}

	return &models.OrderBookDepth{
		Exchange: "mexc",
		Pair:     pair,
		BidDepth: sumMexcDepth(raw.Data.Bids),
		AskDepth: sumMexcDepth(raw.Data.Asks),
	}, nil
}

func sumMexcDepth(levels [][]float64) float64 {
	// NOTE: taken from BTC's contract detail from https://api.mexc.com/api/v1/contract/detail
	const mexcContractSize = 0.0001

	total := 0.0
	for _, level := range levels {
		if len(level) >= 2 {
			price := level[0]
			contracts := level[1]
			total += contracts * mexcContractSize * price // → USDT notional
		}
	}
	return total
}

// MEXC futures uses `TOKEN1_TOKEN2` (e.g. BTC_USDT) format,
// while the rest of the app uses `TOKEN1TOKEN2` (e.g. BTCUSDT).
func toMexcSymbol(pair string) string {
	if len(pair) > 4 && strings.HasSuffix(pair, "USDT") {
		return pair[:len(pair)-4] + "_USDT"
	}
	return pair
}

func (m *MexcAdapter) fetchTicker(ctx context.Context, pair string) (*mexcTicker, error) {
	url := fmt.Sprintf(
		"https://contract.mexc.com/api/v1/contract/ticker?symbol=%s",
		toMexcSymbol(pair),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("mexc ticker: failed to build request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mexc ticker request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mexc ticker: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mexc ticker: failed to read response body: %w", err)
	}

	var raw struct {
		Success bool       `json:"success"`
		Code    int        `json:"code"`
		Data    mexcTicker `json:"data"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("mexc ticker: failed to parse response: %w", err)
	}

	if !raw.Success || raw.Code != 0 {
		return nil, fmt.Errorf("mexc API error code %d", raw.Code)
	}

	return &raw.Data, nil
}
