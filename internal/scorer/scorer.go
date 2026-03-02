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
// This is the only function that makes exchange API calls.
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
// side: "long", "short", or "general" (default if empty).
// positionSize: position size in USDT. Uses DefaultPosition if <= 0.
func (s *Scorer) ScoreAll(rawData []*models.RawExchangeData, positionSize float64, side string) ([]*models.ExchangeScore, error) {
	if positionSize <= 0 {
		positionSize = constants.DefaultPositionUSDT
	}
	if side == "" {
		side = "general"
	}

	if len(rawData) == 0 {
		return nil, fmt.Errorf("no raw data provided to ScoreAll")
	}

	scores := make([]*models.ExchangeScore, 0, len(rawData))
	for _, raw := range rawData {
		scores = append(scores, scoreFromRaw(raw, positionSize, side))
	}

	normalizeVolume(scores)
	normalizeOI(scores)
	normalizeSlippage(scores)

	for _, score := range scores {
		// Trust slippage proportional to volume — low volume = less trustworthy book
		score.SlippageScore *= score.VolumeScore

		// Funding rate component depends on side:
		// - Long: penalize positive funding (you pay), reward negative (you receive)
		// - Short: penalize negative funding (you pay), reward positive (you receive)
		// - General: same as long — mild penalty for positive funding
		var fundingComponent float64
		switch side {
		case "short":
			denom := 1.0 - score.FundingRate*100
			if denom < 0.01 {
				denom = 0.01 // clamp to avoid division by zero
			}
			fundingComponent = 1.0 / denom
		default: // "long" or "general"
			fundingComponent = 1.0 / (1.0 + score.FundingRate*100)
		}

		score.CompositeScore =
			score.VolumeScore*0.40 +
				(1/(1+score.SpreadPct))*0.25 +
				score.OIScore*0.20 +
				score.SlippageScore*0.10 +
				fundingComponent*0.05
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

	// Standard deviation
	variance := 0.0
	for _, h := range raw.FundingHistory {
		diff := h.Rate - avg
		variance += diff * diff
	}
	summary.StdDev30d = math.Sqrt(variance / float64(len(raw.FundingHistory)))

	return summary
}

// ComputeFundingArb takes raw data from all exchanges for a pair and returns all
// directional exchange pairs ranked by funding differential (highest first).
// A positive Differential means shorting on ShortExchange and longing on LongExchange
// captures net positive funding per period.
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

// ComputeSpotPerpBasis computes the spot/perp basis for each exchange and returns
// results sorted by absolute basis descending (largest discrepancy first).
// Exchanges where spot price is unavailable (SpotMidPrice == 0) are excluded.
// Annualized estimates use 30d avg funding rate history rather than the current
// snapshot, since basis is not expected to remain constant.
func ComputeSpotPerpBasis(rawData []*models.RawExchangeData) []*models.BasisResult {
	var results []*models.BasisResult

	for _, raw := range rawData {
		if raw.SpotMidPrice == 0 || raw.Depth.MidPrice == 0 {
			continue
		}

		basisRaw := raw.Depth.MidPrice - raw.SpotMidPrice
		basisPct := (basisRaw / raw.SpotMidPrice) * 100

		// Use historical funding rate as the basis annualization proxy.
		// avg funding rate * periods/year is a better estimate of expected carry cost
		// than extrapolating the current snapshot.
		summary := ComputeFundingSummary(raw)

		annualizedAvg := summary.AvgRate30d * float64(constants.FundingPeriodsPerYear) * 100
		annualizedLow := (summary.AvgRate30d - summary.StdDev30d) * float64(constants.FundingPeriodsPerYear) * 100
		annualizedHigh := (summary.AvgRate30d + summary.StdDev30d) * float64(constants.FundingPeriodsPerYear) * 100

		// Normalize so low <= high always (matters when avg is negative)
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

	// Sort by absolute basis descending — largest discrepancy first
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if math.Abs(results[j].BasisPct) > math.Abs(results[i].BasisPct) {
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

// scoreFromRaw computes an ExchangeScore from raw data for a given side and position size.
// Pure computation — no API calls.
func scoreFromRaw(raw *models.RawExchangeData, positionSize float64, side string) *models.ExchangeScore {
	spreadPct := 0.0
	if raw.Spread.Bid > 0 {
		spreadPct = (raw.Spread.Spread / raw.Spread.Bid) * 100
	}

	// Pick order book side based on trade direction:
	// Long = market buy = walk ask side (prices going up)
	// Short = market sell = walk bid side (prices going down)
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

// estimateSlippage estimates slippage % for a market order of positionUSDT,
// walking the provided order book levels (asks for long, bids for short).
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

func rankScores(scores []*models.ExchangeScore) {
	for i := 0; i < len(scores)-1; i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].CompositeScore > scores[i].CompositeScore {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}
}
