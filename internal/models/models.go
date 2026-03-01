package models

import "time"

type FundingRate struct {
	Exchange    string    `json:"exchange"`
	Pair        string    `json:"pair"`
	Rate        float64   `json:"rate"`
	NextFunding time.Time `json:"next_funding"`
}

type Spread struct {
	Exchange string  `json:"exchange"`
	Pair     string  `json:"pair"`
	Bid      float64 `json:"bid"`
	Ask      float64 `json:"ask"`
	Spread   float64 `json:"spread"` // Ask - Bid
}

type OrderBookLevel struct {
	Price    float64
	Quantity float64 // always in base asset (BTC/ETH), already converted from contracts for MEXC
}

type OrderBookDepth struct {
	Exchange string           `json:"exchange"`
	Pair     string           `json:"pair"`
	BidDepth float64          `json:"bid_depth"` // total liquidity on buy side
	AskDepth float64          `json:"ask_depth"` // total liquidity on sell side
	Bids     []OrderBookLevel // raw levels, closest to mid first
	Asks     []OrderBookLevel // raw levels, closest to mid first
	MidPrice float64          // (best bid + best ask) / 2
}

type MarketStats struct {
	Exchange     string
	Pair         string
	Volume24h    float64 // 24h quote volume in USDT
	OpenInterest float64 // open interest in USDT
}

type ExchangeScore struct {
	Exchange       string    `json:"exchange"`
	Pair           string    `json:"pair"`
	FundingRate    float64   `json:"funding_rate"`
	SpreadPct      float64   `json:"spread_pct"`
	RawBidDepth    float64   `json:"raw_bid_depth"`
	RawAskDepth    float64   `json:"raw_ask_depth"`
	DepthScore     float64   `json:"depth_score"`
	Volume24h      float64   `json:"volume_24h"`
	OpenInterest   float64   `json:"open_interest"`
	VolumeScore    float64   `json:"volume_score"`
	OIScore        float64   `json:"oi_score"`
	CompositeScore float64   `json:"composite_score"`
	UpdatedAt      time.Time `json:"updated_at"`
}
