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
	url := fmt.Sprintf(
		"https://api.bybit.com/v5/market/funding/history?category=linear&symbol=%s&limit=1",
		pair,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("bybit funding rate: failed to build request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bybit funding rate request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Bybit wraps ALL responses in a retCode/result envelope
	var raw struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				Symbol               string `json:"symbol"`
				FundingRate          string `json:"fundingRate"`
				FundingRateTimestamp string `json:"fundingRateTimestamp"`
			} `json:"list"`
		} `json:"result"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse bybit response: %w", err)
	}

	if raw.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error %d: %s", raw.RetCode, raw.RetMsg)
	}

	if len(raw.Result.List) == 0 {
		return nil, fmt.Errorf("bybit returned empty funding rate list for %s", pair)
	}

	entry := raw.Result.List[0]

	rate, err := strconv.ParseFloat(entry.FundingRate, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse funding rate: %w", err)
	}

	tsMs, err := strconv.ParseInt(entry.FundingRateTimestamp, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse funding timestamp: %w", err)
	}

	return &models.FundingRate{
		Exchange:    "bybit",
		Pair:        pair,
		Rate:        rate,
		NextFunding: time.UnixMilli(tsMs),
	}, nil
}

func (b *BybitAdapter) GetSpread(ctx context.Context, pair string) (*models.Spread, error) {
	url := fmt.Sprintf(
		"https://api.bybit.com/v5/market/tickers?category=linear&symbol=%s",
		pair,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("bybit spread: failed to build request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bybit spread request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var raw struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				Bid1Price string `json:"bid1Price"`
				Ask1Price string `json:"ask1Price"`
			} `json:"list"`
		} `json:"result"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse bybit spread response: %w", err)
	}

	if raw.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error %d: %s", raw.RetCode, raw.RetMsg)
	}

	if len(raw.Result.List) == 0 {
		return nil, fmt.Errorf("bybit returned empty ticker list for %s", pair)
	}

	entry := raw.Result.List[0]
	bid, _ := strconv.ParseFloat(entry.Bid1Price, 64)
	ask, _ := strconv.ParseFloat(entry.Ask1Price, 64)

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
