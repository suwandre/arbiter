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
	apiKey        string
	httpClient    *http.Client
	contractSizes map[string]float64 // MEXC calculates orderbooks via contract sizes. This stores the contract size for each pair.
}

type mexcTicker struct {
	Bid1        float64 `json:"bid1"`
	Ask1        float64 `json:"ask1"`
	FundingRate float64 `json:"fundingRate"`
	Timestamp   int64   `json:"timestamp"`
}

func NewMexcAdapter(apiKey string) (*MexcAdapter, error) {
	m := &MexcAdapter{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		contractSizes: make(map[string]float64),
	}

	// fetch contract sizes once
	if err := m.loadContractSizes(context.Background()); err != nil {
		return nil, fmt.Errorf("mexc: failed to load contract sizes: %w", err)
	}

	return m, nil
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
		"https://contract.mexc.com/api/v1/contract/depth/%s?limit=50",
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

	// MEXC depth uses contract sizes to calculate the depth
	contractSize := m.contractSizes[toMexcSymbol(pair)]

	return &models.OrderBookDepth{
		Exchange: "mexc",
		Pair:     pair,
		BidDepth: sumMexcDepth(raw.Data.Bids, contractSize),
		AskDepth: sumMexcDepth(raw.Data.Asks, contractSize),
	}, nil

}

func sumMexcDepth(levels [][]float64, contractSize float64) float64 {
	total := 0.0
	for _, level := range levels {
		if len(level) >= 2 {
			// level[1] = contracts, level[0] = price
			total += level[1] * contractSize * level[0]
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

func (m *MexcAdapter) loadContractSizes(ctx context.Context) error {
	url := "https://contract.mexc.com/api/v1/contract/detail"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("mexc contract detail: failed to build request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mexc contract detail request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("mexc contract detail: failed to read body: %w", err)
	}

	var raw struct {
		Success bool `json:"success"`
		Code    int  `json:"code"`
		Data    []struct {
			Symbol       string  `json:"symbol"`
			ContractSize float64 `json:"contractSize"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("mexc contract detail: failed to parse: %w", err)
	}

	if !raw.Success || raw.Code != 0 {
		return fmt.Errorf("mexc contract detail: API error code %d", raw.Code)
	}

	for _, d := range raw.Data {
		m.contractSizes[d.Symbol] = d.ContractSize
	}

	return nil
}
