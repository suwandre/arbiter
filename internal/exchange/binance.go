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
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/depth?symbol=%s&limit=1000", pair)

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

	bids := parseLevels(raw.Bids)
	asks := parseLevels(raw.Asks)

	midPrice := 0.0
	if len(bids) > 0 && len(asks) > 0 {
		midPrice = (bids[0].Price + asks[0].Price) / 2
	}

	return &models.OrderBookDepth{
		Exchange: "binance",
		Pair:     pair,
		BidDepth: sumDepth(raw.Bids), // keep for now
		AskDepth: sumDepth(raw.Asks),
		Bids:     bids,
		Asks:     asks,
		MidPrice: midPrice,
	}, nil
}

func (b *BinanceAdapter) GetMarketStats(ctx context.Context, pair string) (*models.MarketStats, error) {
	type tickerResult struct {
		volume float64
		vwap   float64
		err    error
	}
	type oiResult struct {
		oiBTC float64
		err   error
	}

	tickerCh := make(chan tickerResult, 1)
	oiCh := make(chan oiResult, 1)

	go func() {
		url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/ticker/24hr?symbol=%s", pair)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			tickerCh <- tickerResult{err: err}
			return
		}
		resp, err := b.httpClient.Do(req)
		if err != nil {
			tickerCh <- tickerResult{err: err}
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		var raw struct {
			QuoteVolume      string `json:"quoteVolume"`
			WeightedAvgPrice string `json:"weightedAvgPrice"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			tickerCh <- tickerResult{err: fmt.Errorf("binance: failed to parse 24hr ticker: %w", err)}
			return
		}
		vol, err := strconv.ParseFloat(raw.QuoteVolume, 64)
		if err != nil {
			tickerCh <- tickerResult{err: fmt.Errorf("binance: failed to parse quoteVolume: %w", err)}
			return
		}
		vwap, err := strconv.ParseFloat(raw.WeightedAvgPrice, 64)
		if err != nil {
			tickerCh <- tickerResult{err: fmt.Errorf("binance: failed to parse weightedAvgPrice: %w", err)}
			return
		}
		tickerCh <- tickerResult{volume: vol, vwap: vwap}
	}()

	go func() {
		url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/openInterest?symbol=%s", pair)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			oiCh <- oiResult{err: err}
			return
		}
		resp, err := b.httpClient.Do(req)
		if err != nil {
			oiCh <- oiResult{err: err}
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		var raw struct {
			OpenInterest string `json:"openInterest"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			oiCh <- oiResult{err: fmt.Errorf("binance: failed to parse open interest: %w", err)}
			return
		}
		oiBTC, err := strconv.ParseFloat(raw.OpenInterest, 64)
		if err != nil {
			oiCh <- oiResult{err: fmt.Errorf("binance: failed to parse openInterest value: %w", err)}
			return
		}
		oiCh <- oiResult{oiBTC: oiBTC}
	}()

	tr := <-tickerCh
	if tr.err != nil {
		return nil, fmt.Errorf("binance market stats (ticker): %w", tr.err)
	}

	or := <-oiCh
	if or.err != nil {
		return nil, fmt.Errorf("binance market stats (OI): %w", or.err)
	}

	return &models.MarketStats{
		Exchange:     "binance",
		Pair:         pair,
		Volume24h:    tr.volume,
		OpenInterest: or.oiBTC * tr.vwap, // convert BTC → USDT using VWAP
	}, nil
}

func (b *BinanceAdapter) GetFundingRateHistory(ctx context.Context, pair string, limit int) ([]models.FundingRateHistory, error) {
	url := fmt.Sprintf(
		"https://fapi.binance.com/fapi/v1/fundingRate?symbol=%s&limit=%d",
		pair, limit,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("binance funding history: failed to build request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("binance funding history request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance funding history: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("binance funding history: failed to read body: %w", err)
	}

	var raw []struct {
		FundingRate string `json:"fundingRate"`
		FundingTime int64  `json:"fundingTime"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("binance funding history: failed to parse response: %w", err)
	}

	history := make([]models.FundingRateHistory, 0, len(raw))
	for _, r := range raw {
		rate, err := strconv.ParseFloat(r.FundingRate, 64)
		if err != nil {
			continue
		}
		history = append(history, models.FundingRateHistory{
			Rate:      rate,
			Timestamp: time.UnixMilli(r.FundingTime),
		})
	}

	return history, nil
}
