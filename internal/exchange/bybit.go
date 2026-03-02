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
	Bid1Price         string `json:"bid1Price"`
	Ask1Price         string `json:"ask1Price"`
	FundingRate       string `json:"fundingRate"`
	NextFundingTime   string `json:"nextFundingTime"`
	Turnover24h       string `json:"turnover24h"`
	OpenInterest      string `json:"openInterest"`
	OpenInterestValue string `json:"openInterestValue"`
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

func (b *BybitAdapter) GetFundingRate(ctx context.Context, pair string) (models.FundingRate, error) {
	ticker, err := b.fetchTicker(ctx, pair)
	if err != nil {
		return models.FundingRate{}, err
	}

	rate, err := strconv.ParseFloat(ticker.FundingRate, 64)
	if err != nil {
		return models.FundingRate{}, fmt.Errorf("failed to parse funding rate: %w", err)
	}

	tsMs, err := strconv.ParseInt(ticker.NextFundingTime, 10, 64)
	if err != nil {
		return models.FundingRate{}, fmt.Errorf("failed to parse next funding time: %w", err)
	}

	return models.FundingRate{
		Exchange:    "bybit",
		Pair:        pair,
		Rate:        rate,
		NextFunding: time.UnixMilli(tsMs),
	}, nil
}

func (b *BybitAdapter) GetSpread(ctx context.Context, pair string) (models.Spread, error) {
	ticker, err := b.fetchTicker(ctx, pair)
	if err != nil {
		return models.Spread{}, err
	}

	bid, _ := strconv.ParseFloat(ticker.Bid1Price, 64)
	ask, _ := strconv.ParseFloat(ticker.Ask1Price, 64)

	return models.Spread{
		Exchange: "bybit",
		Pair:     pair,
		Bid:      bid,
		Ask:      ask,
		Spread:   ask - bid,
	}, nil
}

func (b *BybitAdapter) GetOrderBookDepth(ctx context.Context, pair string) (*models.OrderBookDepth, error) {
	url := fmt.Sprintf(
		"https://api.bybit.com/v5/market/orderbook?category=linear&symbol=%s&limit=1000",
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

	bids := parseLevels(raw.Result.Bids)
	asks := parseLevels(raw.Result.Asks)

	midPrice := 0.0
	if len(bids) > 0 && len(asks) > 0 {
		midPrice = (bids[0].Price + asks[0].Price) / 2
	}

	return &models.OrderBookDepth{
		Exchange: "bybit",
		Pair:     pair,
		BidDepth: sumDepth(raw.Result.Bids),
		AskDepth: sumDepth(raw.Result.Asks),
		Bids:     bids,
		Asks:     asks,
		MidPrice: midPrice,
	}, nil
}

func (b *BybitAdapter) GetMarketStats(ctx context.Context, pair string) (models.MarketStats, error) {
	ticker, err := b.fetchTicker(ctx, pair)
	if err != nil {
		return models.MarketStats{}, err
	}

	volume, err := strconv.ParseFloat(ticker.Turnover24h, 64)
	if err != nil {
		return models.MarketStats{}, fmt.Errorf("bybit: failed to parse turnover24h: %w", err)
	}

	oi, err := strconv.ParseFloat(ticker.OpenInterestValue, 64)
	if err != nil {
		return models.MarketStats{}, fmt.Errorf("bybit: failed to parse open interest value: %w", err)
	}

	return models.MarketStats{
		Exchange:     "bybit",
		Pair:         pair,
		Volume24h:    volume,
		OpenInterest: oi,
	}, nil
}

func (b *BybitAdapter) GetFundingRateHistory(ctx context.Context, pair string, limit int) ([]models.FundingRateHistory, error) {
	url := fmt.Sprintf(
		"https://api.bybit.com/v5/market/funding/history?category=linear&symbol=%s&limit=%d",
		pair, limit,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("bybit funding history: failed to build request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bybit funding history request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bybit funding history: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bybit funding history: failed to read body: %w", err)
	}

	var raw struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				FundingRate          string `json:"fundingRate"`
				FundingRateTimestamp string `json:"fundingRateTimestamp"`
			} `json:"list"`
		} `json:"result"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("bybit funding history: failed to parse response: %w", err)
	}

	if raw.RetCode != 0 {
		return nil, fmt.Errorf("bybit funding history API error %d: %s", raw.RetCode, raw.RetMsg)
	}

	history := make([]models.FundingRateHistory, 0, len(raw.Result.List))
	for _, r := range raw.Result.List {
		rate, err := strconv.ParseFloat(r.FundingRate, 64)
		if err != nil {
			continue
		}
		tsMs, err := strconv.ParseInt(r.FundingRateTimestamp, 10, 64)
		if err != nil {
			continue
		}
		history = append(history, models.FundingRateHistory{
			Rate:      rate,
			Timestamp: time.UnixMilli(tsMs),
		})
	}

	return history, nil
}

func (b *BybitAdapter) GetSpotPrice(ctx context.Context, pair string) (float64, error) {
	url := fmt.Sprintf(
		"https://api.bybit.com/v5/market/tickers?category=spot&symbol=%s",
		pair,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("bybit spot price: failed to build request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("bybit spot price request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("bybit spot price: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("bybit spot price: failed to read body: %w", err)
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
		return 0, fmt.Errorf("bybit spot price: failed to parse response: %w", err)
	}

	if raw.RetCode != 0 {
		return 0, fmt.Errorf("bybit spot price API error %d: %s", raw.RetCode, raw.RetMsg)
	}

	if len(raw.Result.List) == 0 {
		return 0, fmt.Errorf("bybit spot price: empty result for %s", pair)
	}

	bid, _ := strconv.ParseFloat(raw.Result.List[0].Bid1Price, 64)
	ask, _ := strconv.ParseFloat(raw.Result.List[0].Ask1Price, 64)

	if bid == 0 && ask == 0 {
		return 0, fmt.Errorf("bybit spot price: both bid and ask are zero for %s", pair)
	}

	return (bid + ask) / 2, nil
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
