package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/suwandre/arbiter/internal/models"
)

// BinanceAdapter holds any config/state specific to Binance.
type BinanceAdapter struct {
	apiKey     string
	httpClient *http.Client
}

// Constructor function. Creates a new BinanceAdapter instance.
func NewBinanceAdapter(apiKey string) *BinanceAdapter {
	return &BinanceAdapter{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (b *BinanceAdapter) Name() string {
	return "binance"
}

// Fetches the current funding rate for a perpetual futures pair.
func (b *BinanceAdapter) GetFundingRate(ctx context.Context, pair string) (*models.FundingRate, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/premiumIndex?symbol=%s", pair)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("binance funding rate: failed to build request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("binance funding rate request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance funding rate: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// raw struct matching exactly what Binance returns
	var raw struct {
		Symbol          string `json:"symbol"`
		LastFundingRate string `json:"lastFundingRate"`
		NextFundingTime int64  `json:"nextFundingTime"` // Unix ms timestamp
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse binance response: %w", err)
	}

	rate, err := strconv.ParseFloat(raw.LastFundingRate, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse funding rate value: %w", err)
	}

	return &models.FundingRate{
		Exchange:    "binance",
		Pair:        pair,
		Rate:        rate,
		NextFunding: time.UnixMilli(raw.NextFundingTime),
	}, nil
}

// Fetches the current best bid/ask and calculates spread.
func (b *BinanceAdapter) GetSpread(ctx context.Context, pair string) (*models.Spread, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/ticker/bookTicker?symbol=%s", pair)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("binance spread: failed to build request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("binance spread request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance spread: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var raw struct {
		BidPrice string `json:"bidPrice"`
		AskPrice string `json:"askPrice"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse binance spread response: %w", err)
	}

	bid, _ := strconv.ParseFloat(raw.BidPrice, 64)
	ask, _ := strconv.ParseFloat(raw.AskPrice, 64)

	return &models.Spread{
		Exchange: "binance",
		Pair:     pair,
		Bid:      bid,
		Ask:      ask,
		Spread:   ask - bid,
	}, nil
}

// GetOrderBookDepth fetches top-of-book liquidity depth
func (b *BinanceAdapter) GetOrderBookDepth(ctx context.Context, pair string) (*models.OrderBookDepth, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/depth?symbol=%s&limit=5", pair)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("binance depth: failed to build request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("binance depth request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance depth: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var raw struct {
		Bids [][]string `json:"bids"` // each entry: ["price", "quantity"]
		Asks [][]string `json:"asks"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse depth response: %w", err)
	}

	bidDepth := sumDepth(raw.Bids)
	askDepth := sumDepth(raw.Asks)

	return &models.OrderBookDepth{
		Exchange: "binance",
		Pair:     pair,
		BidDepth: bidDepth,
		AskDepth: askDepth,
	}, nil
}

// Private helper — sums total quote value across order book levels.
func sumDepth(levels [][]string) float64 {
	total := 0.0
	for _, level := range levels {
		price, _ := strconv.ParseFloat(level[0], 64)
		qty, _ := strconv.ParseFloat(level[1], 64)
		total += price * qty
	}
	return total
}
