package scorer

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/exchange"
	"github.com/suwandre/arbiter/internal/models"
)

const (
	TargetLevels    = 20
	DefaultPosition = 500_000.0 // default position size in USDT
)

type Scorer struct {
	exchanges []exchange.Exchange
}

type ExchangeResult struct {
	Score *models.ExchangeScore
	Err   error
}

func NewScorer(exchanges []exchange.Exchange) *Scorer {
	return &Scorer{exchanges}
}

func (s *Scorer) ScoreAll(ctx context.Context, pair string, positionSize float64) ([]*models.ExchangeScore, error) {
	if positionSize <= 0 {
		positionSize = DefaultPosition
	}

	results := make(chan ExchangeResult, len(s.exchanges))
	var wg sync.WaitGroup

	for _, ex := range s.exchanges {
		wg.Add(1)
		go func(ex exchange.Exchange) {
			defer wg.Done()
			score, err := fetchAndScore(ctx, ex, pair, positionSize)
			results <- ExchangeResult{Score: score, Err: err}
		}(ex)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var scores []*models.ExchangeScore
	for result := range results {
		if result.Err != nil {
			log.Warn().Err(result.Err).Msg("failed to score exchange, skipping")
			continue
		}
		scores = append(scores, result.Score)
	}

	if len(scores) == 0 {
		return nil, fmt.Errorf("no exchange data available for pair %s", pair)
	}

	normalizeVolume(scores)
	normalizeOI(scores)
	normalizeSlippage(scores)

	for _, score := range scores {
		// Trust slippage proportional to volume — low volume = less trustworthy book
		score.SlippageScore *= score.VolumeScore

		score.CompositeScore =
			score.VolumeScore*0.40 +
				(1/(1+score.SpreadPct))*0.25 +
				score.OIScore*0.20 +
				score.SlippageScore*0.10 +
				(1/(1+score.FundingRate*100))*0.05
	}

	rankScores(scores)
	return scores, nil
}

func fetchAndScore(ctx context.Context, ex exchange.Exchange, pair string, positionSize float64) (*models.ExchangeScore, error) {
	var (
		wg      sync.WaitGroup
		funding *models.FundingRate
		spread  *models.Spread
		depth   *models.OrderBookDepth
		stats   *models.MarketStats

		fundingErr, spreadErr, depthErr, statsErr error
	)

	wg.Add(4)
	go func() { defer wg.Done(); funding, fundingErr = ex.GetFundingRate(ctx, pair) }()
	go func() { defer wg.Done(); spread, spreadErr = ex.GetSpread(ctx, pair) }()
	go func() { defer wg.Done(); depth, depthErr = ex.GetOrderBookDepth(ctx, pair) }()
	go func() { defer wg.Done(); stats, statsErr = ex.GetMarketStats(ctx, pair) }()

	wg.Wait()

	if fundingErr != nil {
		return nil, fmt.Errorf("[%s] funding rate error: %w", ex.Name(), fundingErr)
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

	spreadPct := 0.0
	if spread.Bid > 0 {
		spreadPct = (spread.Spread / spread.Bid) * 100
	}

	slippagePct := estimateSlippage(depth.Asks, depth.MidPrice, positionSize)

	/// LOGGING
	askLevels := len(depth.Asks)
	bidLevels := len(depth.Bids)

	totalAskValue := 0.0
	for _, lvl := range depth.Asks {
		totalAskValue += lvl.Price * lvl.Quantity
	}

	totalBidValue := 0.0
	for _, lvl := range depth.Bids {
		totalBidValue += lvl.Price * lvl.Quantity
	}

	log.Debug().
		Str("exchange", ex.Name()).
		Str("pair", pair).
		Int("ask_levels", askLevels).
		Int("bid_levels", bidLevels).
		Float64("total_ask_value_usdt", totalAskValue).
		Float64("total_bid_value_usdt", totalBidValue).
		Msg("order book depth stats")
		/// END OF LOGS

	return &models.ExchangeScore{
		Exchange:     ex.Name(),
		Pair:         pair,
		FundingRate:  funding.Rate,
		SpreadPct:    spreadPct,
		RawBidDepth:  depth.BidDepth,
		RawAskDepth:  depth.AskDepth,
		SlippagePct:  slippagePct,
		Volume24h:    stats.Volume24h,
		OpenInterest: stats.OpenInterest,
		PositionSize: positionSize,
		UpdatedAt:    time.Now(),
	}, nil
}

// Estimates slippage % for a market buy of positionUSDT,
// walking the ask side of the book.
func estimateSlippage(asks []models.OrderBookLevel, midPrice float64, positionUSDT float64) float64 {
	if len(asks) == 0 || midPrice == 0 {
		return 0
	}

	remaining := positionUSDT
	totalCost := 0.0
	filledBase := 0.0

	for _, lvl := range asks {
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

	if remaining > 0 && len(asks) > 0 {
		lastPrice := asks[len(asks)-1].Price
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

// Normalizes slippage so lower slippage = higher score.
func normalizeSlippage(scores []*models.ExchangeScore) {
	// prevent NaN from floating point near-zero values
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
