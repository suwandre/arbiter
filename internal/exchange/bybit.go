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

type BybitAdapter struct {
	apiKey     string
	httpClient *http.Client
}

type bybitTicker struct {
	Bid1Price       string `json:"bid1Price"`
	Ask1Price       string `json:"ask1Price"`
	FundingRate     string `json:"fundingRate"`
	NextFundingTime string `json:"nextFundingTime"` // Unix ms string
}

func NewBybitAdapter(apiKey string) *BybitAdapter {
	return &BybitAdapter{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (b *BybitAdapter) Name() string {
	return "bybit"
}

func (b *BybitAdapter) GetFundingRate(ctx context.Context, pair string) (*models.FundingRate, error) {
	ticker, err := b.fetchTicker(ctx, pair)
	if err != nil {
		return nil, err
	}

	rate, err := strconv.ParseFloat(ticker.FundingRate, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse funding rate: %w", err)
	}

	tsMs, err := strconv.ParseInt(ticker.NextFundingTime, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse next funding time: %w", err)
	}

	return &models.FundingRate{
		Exchange:    "bybit",
		Pair:        pair,
		Rate:        rate,
		NextFunding: time.UnixMilli(tsMs),
	}, nil
}

func (b *BybitAdapter) GetSpread(ctx context.Context, pair string) (*models.Spread, error) {
	ticker, err := b.fetchTicker(ctx, pair)
	if err != nil {
		return nil, err
	}

	bid, _ := strconv.ParseFloat(ticker.Bid1Price, 64)
	ask, _ := strconv.ParseFloat(ticker.Ask1Price, 64)

	return &models.Spread{
		Exchange: "bybit",
		Pair:     pair,
		Bid:      bid,
		Ask:      ask,
		Spread:   ask - bid,
	}, nil
}

func (b *BybitAdapter) GetOrderBookDepth(ctx context.Context, pair string) (*models.OrderBookDepth, error) {
	url := fmt.Sprintf(
		"https://api.bybit.com/v5/market/orderbook?category=linear&symbol=%s&limit=5",
		pair,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("bybit depth: failed to build request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bybit depth request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bybit depth: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var raw struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			Bids [][]string `json:"b"`
			Asks [][]string `json:"a"`
		} `json:"result"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse bybit depth response: %w", err)
	}

	if raw.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error %d: %s", raw.RetCode, raw.RetMsg)
	}

	return &models.OrderBookDepth{
		Exchange: "bybit",
		Pair:     pair,
		BidDepth: sumDepth(raw.Result.Bids),
		AskDepth: sumDepth(raw.Result.Asks),
	}, nil
}

func (b *BybitAdapter) fetchTicker(ctx context.Context, pair string) (*bybitTicker, error) {
	url := fmt.Sprintf(
		"https://api.bybit.com/v5/market/tickers?category=linear&symbol=%s",
		pair,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("bybit ticker: failed to build request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bybit ticker request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bybit ticker: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var raw struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []bybitTicker `json:"list"`
		} `json:"result"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse bybit ticker response: %w", err)
	}

	if raw.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error %d: %s", raw.RetCode, raw.RetMsg)
	}

	if len(raw.Result.List) == 0 {
		return nil, fmt.Errorf("bybit returned empty ticker list for %s", pair)
	}

	return &raw.Result.List[0], nil
}
