package scorer

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/constants"
	"github.com/suwandre/arbiter/internal/exchange"
	"github.com/suwandre/arbiter/internal/models"
)

// ScoringMode identifies which use-case weight profile to apply.
type ScoringMode string

const (
	ModeEntryLong      ScoringMode = "entry_long"
	ModeEntryShort     ScoringMode = "entry_short"
	ModeLargePosition  ScoringMode = "large_position"
	ModeSmallPosition  ScoringMode = "small_position"
	ModeOvernightLong  ScoringMode = "overnight_long"
	ModeOvernightShort ScoringMode = "overnight_short"
	ModeExitFast       ScoringMode = "exit_fast"
	ModeGeneral        ScoringMode = "general"
)

// ScoreWeights defines how much each signal contributes to the composite score.
// All weights must sum to 1.0.
type ScoreWeights struct {
	Volume   float64 // 24h volume (normalised)
	Spread   float64 // bid/ask spread (lower is better)
	OI       float64 // open interest (normalised)
	Slippage float64 // estimated market impact (lower is better)
	Funding  float64 // funding rate component (side-adjusted)
	BidDepth float64 // raw bid-side depth (relevant for exit / large position)
}

// weightProfiles maps each ScoringMode to its weight set.
// Rationale per profile:
//
//	entry_long / entry_short
//	  Balanced entry. Spread and slippage matter for entry quality,
//	  volume/OI give confidence in liquidity depth.
//	  Funding is a mild tiebreaker — you care but it's not the primary driver.
//
//	large_position
//	  Slippage and depth dominate — a large order needs a deep book.
//	  Spread matters less since the fill will walk the book anyway.
//	  Funding is nearly irrelevant for a single entry decision.
//
//	small_position
//	  Spread dominates — a small order fills at or near the top of book.
//	  Slippage is essentially irrelevant at small size.
//	  Volume/OI still provide some confidence signal.
//
//	overnight_long / overnight_short
//	  Funding is the primary cost of carry for multi-day holds.
//	  Everything else is a minor tiebreaker.
//
//	exit_fast
//	  Bid depth and slippage on the exit side dominate — you need to
//	  get out NOW at minimal impact. Funding is irrelevant.
//
//	general
//	  Balanced default. Same as the previous hardcoded formula so
//	  existing callers without a mode param see identical results.
var weightProfiles = map[ScoringMode]ScoreWeights{
	ModeEntryLong: {
		Volume:   0.25,
		Spread:   0.30,
		OI:       0.20,
		Slippage: 0.20,
		Funding:  0.05,
		BidDepth: 0.00,
	},
	ModeEntryShort: {
		Volume:   0.25,
		Spread:   0.30,
		OI:       0.20,
		Slippage: 0.20,
		Funding:  0.05,
		BidDepth: 0.00,
	},
	ModeLargePosition: {
		Volume:   0.15,
		Spread:   0.05,
		OI:       0.20,
		Slippage: 0.40,
		Funding:  0.00,
		BidDepth: 0.20,
	},
	ModeSmallPosition: {
		Volume:   0.20,
		Spread:   0.55,
		OI:       0.15,
		Slippage: 0.05,
		Funding:  0.05,
		BidDepth: 0.00,
	},
	ModeOvernightLong: {
		Volume:   0.05,
		Spread:   0.05,
		OI:       0.05,
		Slippage: 0.05,
		Funding:  0.80,
		BidDepth: 0.00,
	},
	ModeOvernightShort: {
		Volume:   0.05,
		Spread:   0.05,
		OI:       0.05,
		Slippage: 0.05,
		Funding:  0.80,
		BidDepth: 0.00,
	},
	ModeExitFast: {
		Volume:   0.10,
		Spread:   0.10,
		OI:       0.05,
		Slippage: 0.35,
		Funding:  0.00,
		BidDepth: 0.40,
	},
	ModeGeneral: {
		Volume:   0.40,
		Spread:   0.25,
		OI:       0.20,
		Slippage: 0.10,
		Funding:  0.05,
		BidDepth: 0.00,
	},
}

// ParseScoringMode converts a raw query param string into a ScoringMode.
// Returns ModeGeneral and false if the value is unrecognised.
func ParseScoringMode(raw string) (ScoringMode, bool) {
	switch ScoringMode(raw) {
	case ModeEntryLong, ModeEntryShort, ModeLargePosition, ModeSmallPosition,
		ModeOvernightLong, ModeOvernightShort, ModeExitFast, ModeGeneral:
		return ScoringMode(raw), true
	case "":
		return ModeGeneral, true
	default:
		return ModeGeneral, false
	}
}

// ValidScoringModes returns all accepted mode strings for use in error messages.
func ValidScoringModes() []string {
	return []string{
		string(ModeEntryLong),
		string(ModeEntryShort),
		string(ModeLargePosition),
		string(ModeSmallPosition),
		string(ModeOvernightLong),
		string(ModeOvernightShort),
		string(ModeExitFast),
		string(ModeGeneral),
	}
}

// sideForMode derives the order book side to walk from the scoring mode.
// Modes that imply a direction override the caller-supplied side.
func sideForMode(mode ScoringMode, side string) string {
	switch mode {
	case ModeEntryLong, ModeOvernightLong:
		return "long"
	case ModeEntryShort, ModeOvernightShort:
		return "short"
	case ModeExitFast:
		// Exiting a long = sell into bids; treat as short-side walk.
		// Caller should pass side=long to indicate they are exiting a long.
		// We flip it here so the order book walk uses the bid side.
		if side == "long" {
			return "short"
		}
		return "long"
	default:
		return side
	}
}

type Scorer struct {
	exchanges []exchange.Exchange
}

type fetchResult struct {
	data *models.RawExchangeData
	err  error
}

func NewScorer(exchanges []exchange.Exchange) *Scorer {
	return &Scorer{exchanges}
}

// FetchAll fetches raw market data from all exchanges for a given pair concurrently.
func (s *Scorer) FetchAll(ctx context.Context, pair string) ([]*models.RawExchangeData, error) {
	results := make(chan fetchResult, len(s.exchanges))
	var wg sync.WaitGroup

	for _, ex := range s.exchanges {
		wg.Add(1)
		go func(ex exchange.Exchange) {
			defer wg.Done()
			data, err := fetchRawData(ctx, ex, pair)
			results <- fetchResult{data: data, err: err}
		}(ex)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var allData []*models.RawExchangeData
	for result := range results {
		if result.err != nil {
			log.Warn().Err(result.err).Msg("failed to fetch exchange data, skipping")
			continue
		}
		allData = append(allData, result.data)
	}

	if len(allData) == 0 {
		return nil, fmt.Errorf("no exchange data available for pair %s", pair)
	}

	return allData, nil
}

// ScoreAll derives scores from already-fetched raw data. No API calls.
// mode: scoring profile to apply (see ScoringMode constants). Defaults to ModeGeneral.
// side: "long", "short", or "general" — used for funding direction and order book walk
//
//	when the mode does not imply a fixed side.
//
// positionSize: position size in USDT. Uses DefaultPositionUSDT if <= 0.
func (s *Scorer) ScoreAll(rawData []*models.RawExchangeData, positionSize float64, side string, mode ScoringMode) ([]*models.ExchangeScore, error) {
	if positionSize <= 0 {
		positionSize = constants.DefaultPositionUSDT
	}
	if side == "" {
		side = "general"
	}
	if mode == "" {
		mode = ModeGeneral
	}

	if len(rawData) == 0 {
		return nil, fmt.Errorf("no raw data provided to ScoreAll")
	}

	weights, ok := weightProfiles[mode]
	if !ok {
		weights = weightProfiles[ModeGeneral]
	}

	// Resolve the effective order book side to walk.
	effectiveSide := sideForMode(mode, side)

	scores := make([]*models.ExchangeScore, 0, len(rawData))
	for _, raw := range rawData {
		scores = append(scores, scoreFromRaw(raw, positionSize, effectiveSide, string(mode)))
	}

	normalizeVolume(scores)
	normalizeOI(scores)
	normalizeSlippage(scores)
	normalizeBidDepth(scores)

	for _, score := range scores {
		// Slippage trustworthiness scales with volume
		score.SlippageScore *= score.VolumeScore

		// Funding component is side-sensitive
		var fundingComponent float64
		switch effectiveSide {
		case "short":
			denom := 1.0 - score.FundingRate*100
			if denom < 0.01 {
				denom = 0.01
			}
			fundingComponent = 1.0 / denom
		default: // long or general
			fundingComponent = 1.0 / (1.0 + score.FundingRate*100)
		}

		score.CompositeScore =
			score.VolumeScore*weights.Volume +
				(1/(1+score.SpreadPct))*weights.Spread +
				score.OIScore*weights.OI +
				score.SlippageScore*weights.Slippage +
				fundingComponent*weights.Funding +
				score.BidDepthScore*weights.BidDepth
	}

	rankScores(scores)
	return scores, nil
}

// ComputeFundingSummary derives stats from historical funding data for a single exchange.
func ComputeFundingSummary(raw *models.RawExchangeData) models.FundingRateSummary {
	summary := models.FundingRateSummary{
		CurrentRate: raw.Funding.Rate,
		Periods:     len(raw.FundingHistory),
	}

	if len(raw.FundingHistory) == 0 {
		return summary
	}

	sum := 0.0
	min := raw.FundingHistory[0].Rate
	max := raw.FundingHistory[0].Rate

	for _, h := range raw.FundingHistory {
		sum += h.Rate
		if h.Rate < min {
			min = h.Rate
		}
		if h.Rate > max {
			max = h.Rate
		}
	}

	avg := sum / float64(len(raw.FundingHistory))
	summary.AvgRate30d = avg
	summary.MinRate30d = min
	summary.MaxRate30d = max

	variance := 0.0
	for _, h := range raw.FundingHistory {
		diff := h.Rate - avg
		variance += diff * diff
	}
	summary.StdDev30d = math.Sqrt(variance / float64(len(raw.FundingHistory)))

	return summary
}

// ComputeFundingArb returns all directional exchange pairs ranked by funding differential.
func ComputeFundingArb(rawData []*models.RawExchangeData) []*models.FundingArbPair {
	var pairs []*models.FundingArbPair

	for i := 0; i < len(rawData); i++ {
		for j := 0; j < len(rawData); j++ {
			if i == j {
				continue
			}
			a := rawData[i]
			b := rawData[j]

			diff := b.Funding.Rate - a.Funding.Rate
			if diff <= 0 {
				continue
			}

			pairs = append(pairs, &models.FundingArbPair{
				LongExchange:  a.Exchange,
				ShortExchange: b.Exchange,
				LongRate:      a.Funding.Rate,
				ShortRate:     b.Funding.Rate,
				Differential:  diff,
				DiffPct:       diff * 100,
				Annualized:    diff * 100 * constants.FundingPeriodsPerYear,
			})
		}
	}

	for i := 0; i < len(pairs)-1; i++ {
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].Differential > pairs[i].Differential {
				pairs[i], pairs[j] = pairs[j], pairs[i]
			}
		}
	}

	return pairs
}

// ComputeSpotPerpBasis computes the spot/perp basis per exchange, sorted by absolute basis descending.
func ComputeSpotPerpBasis(rawData []*models.RawExchangeData) []*models.BasisResult {
	var results []*models.BasisResult

	for _, raw := range rawData {
		if raw.SpotMidPrice == 0 || raw.Depth.MidPrice == 0 {
			continue
		}

		basisRaw := raw.Depth.MidPrice - raw.SpotMidPrice
		basisPct := (basisRaw / raw.SpotMidPrice) * 100

		summary := ComputeFundingSummary(raw)

		annualizedAvg := summary.AvgRate30d * float64(constants.FundingPeriodsPerYear) * 100
		annualizedLow := (summary.AvgRate30d - summary.StdDev30d) * float64(constants.FundingPeriodsPerYear) * 100
		annualizedHigh := (summary.AvgRate30d + summary.StdDev30d) * float64(constants.FundingPeriodsPerYear) * 100

		if annualizedLow > annualizedHigh {
			annualizedLow, annualizedHigh = annualizedHigh, annualizedLow
		}

		results = append(results, &models.BasisResult{
			Exchange:       raw.Exchange,
			PerpMidPrice:   raw.Depth.MidPrice,
			SpotMidPrice:   raw.SpotMidPrice,
			BasisRaw:       basisRaw,
			BasisPct:       basisPct,
			AnnualizedAvg:  annualizedAvg,
			AnnualizedLow:  annualizedLow,
			AnnualizedHigh: annualizedHigh,
			HistoryPeriods: summary.Periods,
			UpdatedAt:      raw.FetchedAt,
		})
	}

	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if math.Abs(results[j].BasisPct) > math.Abs(results[i].BasisPct) {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	return results
}

// ComputeCrossBasisOpportunities finds all cross-exchange basis trade opportunities.
func ComputeCrossBasisOpportunities(rawData []*models.RawExchangeData, feeOverrides map[string]models.ExchangeFees) []*models.CrossBasisOpportunity {
	var results []*models.CrossBasisOpportunity

	for i := 0; i < len(rawData); i++ {
		for j := 0; j < len(rawData); j++ {
			if i == j {
				continue
			}

			spotEx := rawData[i]
			perpEx := rawData[j]

			if spotEx.SpotMidPrice == 0 || perpEx.Depth.MidPrice == 0 {
				continue
			}

			perpEntryPrice := perpEx.Depth.MidPrice
			if len(perpEx.Depth.Bids) > 0 {
				perpEntryPrice = perpEx.Depth.Bids[0].Price
			}
			spotEntryPrice := spotEx.SpotMidPrice

			grossBasisPct := (perpEntryPrice - spotEntryPrice) / spotEntryPrice * 100

			spotFees := constants.DefaultTakerFees[spotEx.Exchange]
			if override, ok := feeOverrides[spotEx.Exchange]; ok {
				spotFees = override
			}
			perpFees := constants.DefaultTakerFees[perpEx.Exchange]
			if override, ok := feeOverrides[perpEx.Exchange]; ok {
				perpFees = override
			}

			netBasisPct := grossBasisPct - spotFees.SpotTakerPct - perpFees.PerpTakerPct

			summary := ComputeFundingSummary(perpEx)
			annualizedAvg := summary.AvgRate30d * float64(constants.FundingPeriodsPerYear) * 100
			annualizedLow := (summary.AvgRate30d - summary.StdDev30d) * float64(constants.FundingPeriodsPerYear) * 100
			annualizedHigh := (summary.AvgRate30d + summary.StdDev30d) * float64(constants.FundingPeriodsPerYear) * 100
			if annualizedLow > annualizedHigh {
				annualizedLow, annualizedHigh = annualizedHigh, annualizedLow
			}

			results = append(results, &models.CrossBasisOpportunity{
				SpotExchange:   spotEx.Exchange,
				PerpExchange:   perpEx.Exchange,
				SpotEntryPrice: spotEntryPrice,
				PerpEntryPrice: perpEntryPrice,
				GrossBasisPct:  grossBasisPct,
				SpotFeePct:     spotFees.SpotTakerPct,
				PerpFeePct:     perpFees.PerpTakerPct,
				NetBasisPct:    netBasisPct,
				AnnualizedAvg:  annualizedAvg,
				AnnualizedLow:  annualizedLow,
				AnnualizedHigh: annualizedHigh,
				Viable:         netBasisPct > 0,
				UpdatedAt:      time.Now(),
			})
		}
	}

	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].NetBasisPct > results[i].NetBasisPct {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	return results
}

// fetchRawData fetches all market data for one exchange+pair concurrently.
func fetchRawData(ctx context.Context, ex exchange.Exchange, pair string) (*models.RawExchangeData, error) {
	var (
		wg      sync.WaitGroup
		funding models.FundingRate
		history []models.FundingRateHistory
		spread  models.Spread
		depth   *models.OrderBookDepth
		stats   models.MarketStats
		spotMid float64

		fundingErr, historyErr, spreadErr, depthErr, statsErr, spotErr error
	)

	wg.Add(6)
	go func() { defer wg.Done(); funding, fundingErr = ex.GetFundingRate(ctx, pair) }()
	go func() { defer wg.Done(); history, historyErr = ex.GetFundingRateHistory(ctx, pair, 90) }()
	go func() { defer wg.Done(); spread, spreadErr = ex.GetSpread(ctx, pair) }()
	go func() { defer wg.Done(); depth, depthErr = ex.GetOrderBookDepth(ctx, pair) }()
	go func() { defer wg.Done(); stats, statsErr = ex.GetMarketStats(ctx, pair) }()
	go func() { defer wg.Done(); spotMid, spotErr = ex.GetSpotPrice(ctx, pair) }()

	wg.Wait()

	if fundingErr != nil {
		return nil, fmt.Errorf("[%s] funding rate error: %w", ex.Name(), fundingErr)
	}
	if historyErr != nil {
		log.Warn().Err(historyErr).Str("exchange", ex.Name()).Str("pair", pair).Msg("failed to fetch funding history, continuing without it")
	}
	if spreadErr != nil {
		return nil, fmt.Errorf("[%s] spread error: %w", ex.Name(), spreadErr)
	}
	if depthErr != nil {
		return nil, fmt.Errorf("[%s] depth error: %w", ex.Name(), depthErr)
	}
	if statsErr != nil {
		return nil, fmt.Errorf("[%s] market stats error: %w", ex.Name(), statsErr)
	}
	if spotErr != nil {
		log.Warn().Err(spotErr).Str("exchange", ex.Name()).Str("pair", pair).Msg("failed to fetch spot price, basis will be unavailable")
	}

	log.Debug().
		Str("exchange", ex.Name()).
		Str("pair", pair).
		Int("ask_levels", len(depth.Asks)).
		Int("bid_levels", len(depth.Bids)).
		Int("funding_history_periods", len(history)).
		Float64("total_ask_value_usdt", totalBookValue(depth.Asks)).
		Float64("total_bid_value_usdt", totalBookValue(depth.Bids)).
		Float64("spot_mid_price", spotMid).
		Msg("order book depth stats")

	return &models.RawExchangeData{
		Exchange:       ex.Name(),
		Pair:           pair,
		Funding:        funding,
		FundingHistory: history,
		Spread:         spread,
		Depth:          depth,
		Stats:          stats,
		SpotMidPrice:   spotMid,
		FetchedAt:      time.Now(),
	}, nil
}

// scoreFromRaw computes an ExchangeScore from raw data. Pure computation — no API calls.
func scoreFromRaw(raw *models.RawExchangeData, positionSize float64, side string, mode string) *models.ExchangeScore {
	spreadPct := 0.0
	if raw.Spread.Bid > 0 && raw.Spread.Ask > 0 && raw.Spread.Ask > raw.Spread.Bid {
		spreadPct = (raw.Spread.Spread / raw.Spread.Bid) * 100
	}

	var levels []models.OrderBookLevel
	if side == "short" {
		levels = raw.Depth.Bids
	} else {
		levels = raw.Depth.Asks
	}
	slippagePct := estimateSlippage(levels, raw.Depth.MidPrice, positionSize)

	return &models.ExchangeScore{
		Exchange:     raw.Exchange,
		Pair:         raw.Pair,
		Side:         side,
		Mode:         mode,
		FundingRate:  raw.Funding.Rate,
		SpreadPct:    spreadPct,
		RawBidDepth:  raw.Depth.BidDepth,
		RawAskDepth:  raw.Depth.AskDepth,
		SlippagePct:  slippagePct,
		Volume24h:    raw.Stats.Volume24h,
		OpenInterest: raw.Stats.OpenInterest,
		PositionSize: positionSize,
		UpdatedAt:    raw.FetchedAt,
	}
}

func estimateSlippage(levels []models.OrderBookLevel, midPrice float64, positionUSDT float64) float64 {
	if len(levels) == 0 || midPrice == 0 {
		return 0
	}

	remaining := positionUSDT
	totalCost := 0.0
	filledBase := 0.0

	for _, lvl := range levels {
		levelValue := lvl.Price * lvl.Quantity
		if remaining <= levelValue {
			filledBase += remaining / lvl.Price
			totalCost += remaining
			remaining = 0
			break
		}
		totalCost += levelValue
		filledBase += lvl.Quantity
		remaining -= levelValue
	}

	if remaining > 0 && len(levels) > 0 {
		lastPrice := levels[len(levels)-1].Price
		filledBase += remaining / lastPrice
		totalCost += remaining
	}

	if filledBase == 0 {
		return 0
	}

	avgFillPrice := totalCost / filledBase
	slippage := (avgFillPrice - midPrice) / midPrice * 100

	if slippage < 0 {
		return 0
	}
	return slippage
}

func totalBookValue(levels []models.OrderBookLevel) float64 {
	total := 0.0
	for _, lvl := range levels {
		total += lvl.Price * lvl.Quantity
	}
	return total
}

func normalizeSlippage(scores []*models.ExchangeScore) {
	const epsilon = 1e-9
	max := scores[0].SlippagePct
	for _, s := range scores[1:] {
		if s.SlippagePct > max {
			max = s.SlippagePct
		}
	}
	for _, s := range scores {
		if max < epsilon {
			s.SlippageScore = 1.0
		} else {
			s.SlippageScore = 1.0 - math.Sqrt(s.SlippagePct/max)
		}
	}
}

func normalizeVolume(scores []*models.ExchangeScore) {
	max := scores[0].Volume24h
	for _, s := range scores[1:] {
		if s.Volume24h > max {
			max = s.Volume24h
		}
	}
	for _, s := range scores {
		if max == 0 {
			s.VolumeScore = 0
		} else {
			s.VolumeScore = math.Sqrt(s.Volume24h / max)
		}
	}
}

func normalizeOI(scores []*models.ExchangeScore) {
	max := scores[0].OpenInterest
	for _, s := range scores[1:] {
		if s.OpenInterest > max {
			max = s.OpenInterest
		}
	}
	for _, s := range scores {
		if max == 0 {
			s.OIScore = 0
		} else {
			s.OIScore = math.Sqrt(s.OpenInterest / max)
		}
	}
}

// normalizeBidDepth normalises raw bid depth so it can be used as a 0–1 score.
// Used by exit_fast and large_position modes.
func normalizeBidDepth(scores []*models.ExchangeScore) {
	max := scores[0].RawBidDepth
	for _, s := range scores[1:] {
		if s.RawBidDepth > max {
			max = s.RawBidDepth
		}
	}
	for _, s := range scores {
		if max == 0 {
			s.BidDepthScore = 0
		} else {
			s.BidDepthScore = math.Sqrt(s.RawBidDepth / max)
		}
	}
}

func rankScores(scores []*models.ExchangeScore) {
	for i := 0; i < len(scores)-1; i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].CompositeScore > scores[i].CompositeScore {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}
}
