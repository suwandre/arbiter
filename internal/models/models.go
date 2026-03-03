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

// FundingRateHistory represents a single historical funding rate record.
type FundingRateHistory struct {
	Rate      float64   `json:"rate"`
	Timestamp time.Time `json:"timestamp"`
}

// FundingRateSummary holds computed stats from historical funding data.
type FundingRateSummary struct {
	CurrentRate float64 `json:"current_rate"`
	AvgRate30d  float64 `json:"avg_rate_30d"` // 30-day average
	StdDev30d   float64 `json:"std_dev_30d"`  // standard deviation — indicates volatility
	MinRate30d  float64 `json:"min_rate_30d"`
	MaxRate30d  float64 `json:"max_rate_30d"`
	Periods     int     `json:"periods"` // number of data points used
}

// FundingArbPair represents the funding rate differential between two exchanges for the same pair.
type FundingArbPair struct {
	LongExchange  string  `json:"long_exchange"`  // exchange where you go long (lower/more negative funding = you receive or pay less)
	ShortExchange string  `json:"short_exchange"` // exchange where you go short (higher/more positive funding = you receive)
	LongRate      float64 `json:"long_rate"`      // current funding rate on the long exchange
	ShortRate     float64 `json:"short_rate"`     // current funding rate on the short exchange
	Differential  float64 `json:"differential"`   // ShortRate - LongRate (gross funding capture per period, as a fraction)
	DiffPct       float64 `json:"diff_pct"`       // Differential * 100, for display
	Annualized    float64 `json:"annualized"`     // DiffPct * periods_per_year (based on 8h intervals = 3/day * 365)
}

// RawExchangeData holds one full fetch of market data for a single exchange+pair.
type RawExchangeData struct {
	Exchange       string
	Pair           string
	Funding        FundingRate          // value: small, immutable, no need for pointer
	FundingHistory []FundingRateHistory // last 90 periods (~30 days)
	Spread         Spread               // value: small, immutable, no need for pointer
	Depth          *OrderBookDepth      // pointer: large nested slices, accessed frequently
	Stats          MarketStats          // value: small, immutable, no need for pointer
	SpotMidPrice   float64
	FetchedAt      time.Time
}

// BasisResult holds the spot/perp basis for a single exchange.
type BasisResult struct {
	Exchange     string  `json:"exchange"`
	PerpMidPrice float64 `json:"perp_mid_price"`
	SpotMidPrice float64 `json:"spot_mid_price"`
	BasisRaw     float64 `json:"basis_raw"` // perp - spot, in USDT
	BasisPct     float64 `json:"basis_pct"` // (perp - spot) / spot * 100

	// Annualized estimates derived from 30d avg funding rate history.
	// More meaningful than extrapolating the current snapshot.
	AnnualizedAvg  float64 `json:"annualized_avg_pct"`  // avg funding rate * periods/year * 100
	AnnualizedLow  float64 `json:"annualized_low_pct"`  // (avg - 1 stddev) * periods/year * 100
	AnnualizedHigh float64 `json:"annualized_high_pct"` // (avg + 1 stddev) * periods/year * 100
	HistoryPeriods int     `json:"history_periods"`     // number of funding periods used

	UpdatedAt time.Time `json:"updated_at"`
}

// CrossBasisOpportunity represents a delta-neutral cross-exchange basis trade:
// buy spot on SpotExchange, short perp on PerpExchange.
// Profitable when the perp premium on PerpExchange exceeds execution costs.
type CrossBasisOpportunity struct {
	SpotExchange string `json:"spot_exchange"`
	PerpExchange string `json:"perp_exchange"`

	SpotEntryPrice float64 `json:"spot_entry_price"`
	PerpEntryPrice float64 `json:"perp_entry_price"`

	GrossBasisPct float64 `json:"gross_basis_pct"`
	SpotFeePct    float64 `json:"spot_fee_pct"`  // taker fee assumed for spot leg
	PerpFeePct    float64 `json:"perp_fee_pct"`  // taker fee assumed for perp leg
	NetBasisPct   float64 `json:"net_basis_pct"` // gross - spot_fee - perp_fee

	AnnualizedAvg  float64 `json:"annualized_avg_pct"`
	AnnualizedLow  float64 `json:"annualized_low_pct"`
	AnnualizedHigh float64 `json:"annualized_high_pct"`

	Viable    bool      `json:"viable"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ExchangeScore struct {
	Exchange       string    `json:"exchange"`
	Pair           string    `json:"pair"`
	Side           string    `json:"side"` // "long", "short", or "general"
	Mode           string    `json:"mode"`
	FundingRate    float64   `json:"funding_rate"`
	SpreadPct      float64   `json:"spread_pct"`
	RawBidDepth    float64   `json:"raw_bid_depth"`
	RawAskDepth    float64   `json:"raw_ask_depth"`
	SlippagePct    float64   `json:"slippage_pct"`   // raw estimated slippage %
	SlippageScore  float64   `json:"slippage_score"` // normalized, higher = better
	BidDepthScore  float64   `json:"bid_depth_score"`
	Volume24h      float64   `json:"volume_24h"`
	OpenInterest   float64   `json:"open_interest"`
	VolumeScore    float64   `json:"volume_score"`
	OIScore        float64   `json:"oi_score"`
	CompositeScore float64   `json:"composite_score"`
	PositionSize   float64   `json:"position_size"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ExchangeFees struct {
	SpotTakerPct float64 `json:"spot_taker_pct"`
	PerpTakerPct float64 `json:"perp_taker_pct"`
}
