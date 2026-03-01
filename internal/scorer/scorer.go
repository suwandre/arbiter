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

// TargetLevels controls depth weighting in the order book scorer.
// The top N levels are weighted linearly: level 0 gets full weight (1.0),
// level N-1 gets near-zero weight. Levels beyond N are ignored entirely.
const TargetLevels = 20

type Scorer struct {
	exchanges []exchange.Exchange
}

// Holds either a score or an error for one exchange.
type ExchangeResult struct {
	Score *models.ExchangeScore
	Err   error
}

func NewScorer(exchanges []exchange.Exchange) *Scorer {
	return &Scorer{exchanges}
}

// Fetches data from all exchanges concurrently for a given pair
// and returns a ranked slice of ExchangeScores.
func (s *Scorer) ScoreAll(ctx context.Context, pair string) ([]*models.ExchangeScore, error) {
	results := make(chan ExchangeResult, len(s.exchanges))

	var wg sync.WaitGroup

	for _, ex := range s.exchanges {
		wg.Add(1)

		go func(ex exchange.Exchange) {
			defer wg.Done()

			score, err := fetchAndScore(ctx, ex, pair)
			results <- ExchangeResult{Score: score, Err: err}
		}(ex)
	}

	// Close the channel once all goroutines finish
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

	// Normalize depth across exchanges before scoring
	normalizeDepth(scores)
	normalizeVolume(scores)
	normalizeOI(scores)

	for _, score := range scores {
		score.CompositeScore =
			score.VolumeScore*0.35 +
				(1/(1+score.SpreadPct))*0.25 +
				score.OIScore*0.20 +
				score.DepthScore*0.15 +
				(1/(1+score.FundingRate*100))*0.05
	}

	rankScores(scores)
	return scores, nil
}

// Calls all three data endpoints for one exchange and computes its score.
func fetchAndScore(ctx context.Context, ex exchange.Exchange, pair string) (*models.ExchangeScore, error) {
	funding, err := ex.GetFundingRate(ctx, pair)
	if err != nil {
		return nil, fmt.Errorf("[%s] funding rate error: %w", ex.Name(), err)
	}

	spread, err := ex.GetSpread(ctx, pair)
	if err != nil {
		return nil, fmt.Errorf("[%s] spread error: %w", ex.Name(), err)
	}

	depth, err := ex.GetOrderBookDepth(ctx, pair)
	if err != nil {
		return nil, fmt.Errorf("[%s] depth error: %w", ex.Name(), err)
	}

	stats, err := ex.GetMarketStats(ctx, pair)
	if err != nil {
		return nil, fmt.Errorf("[%s] market stats error: %w", ex.Name(), err)
	}

	spreadPct := 0.0
	if spread.Bid > 0 {
		spreadPct = (spread.Spread / spread.Bid) * 100
	}

	weightedBid := weightedDepth(depth.Bids)
	weightedAsk := weightedDepth(depth.Asks)

	return &models.ExchangeScore{
		Exchange:     ex.Name(),
		Pair:         pair,
		FundingRate:  funding.Rate,
		SpreadPct:    spreadPct,
		RawBidDepth:  depth.BidDepth,
		RawAskDepth:  depth.AskDepth,
		Volume24h:    stats.Volume24h,
		OpenInterest: stats.OpenInterest,
		DepthScore:   weightedBid + weightedAsk, // normalized later
		// VolumeScore and OIScore computed later after normalization
		UpdatedAt: time.Now(),
	}, nil
}

// Sorts scores in-place, highest CompositeScore first.
func rankScores(scores []*models.ExchangeScore) {
	for i := 0; i < len(scores)-1; i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].CompositeScore > scores[i].CompositeScore {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}
}

// Normalizes depth scores so they range from 0 to 1.
func normalizeDepth(scores []*models.ExchangeScore) {
	minD, maxD := scores[0].DepthScore, scores[0].DepthScore
	for _, s := range scores[1:] {
		if s.DepthScore < minD {
			minD = s.DepthScore
		}
		if s.DepthScore > maxD {
			maxD = s.DepthScore
		}
	}

	for _, s := range scores {
		if maxD == minD {
			s.DepthScore = 1.0 // all equal, give full score
		} else {
			s.DepthScore = (s.DepthScore - minD) / (maxD - minD)
		}
	}
}

func weightedDepth(levels []models.OrderBookLevel) float64 {
	total := 0.0
	for i, lvl := range levels {
		if i >= TargetLevels {
			break
		}
		weight := 1.0 - (float64(i) / float64(TargetLevels))
		total += lvl.Price * lvl.Quantity * weight
	}
	return total
}

func normalizeVolume(scores []*models.ExchangeScore) {
	min, max := scores[0].Volume24h, scores[0].Volume24h
	for _, s := range scores[1:] {
		if s.Volume24h < min {
			min = s.Volume24h
		}
		if s.Volume24h > max {
			max = s.Volume24h
		}
	}
	for _, s := range scores {
		if max == min {
			s.VolumeScore = 1.0
		} else {
			s.VolumeScore = math.Log1p(s.Volume24h-min) / math.Log1p(max-min)
		}
	}
}

func normalizeOI(scores []*models.ExchangeScore) {
	min, max := scores[0].OpenInterest, scores[0].OpenInterest
	for _, s := range scores[1:] {
		if s.OpenInterest < min {
			min = s.OpenInterest
		}
		if s.OpenInterest > max {
			max = s.OpenInterest
		}
	}
	for _, s := range scores {
		if max == min {
			s.OIScore = 1.0
		} else {
			s.OIScore = math.Log1p(s.OpenInterest-min) / math.Log1p(max-min)
		}
	}
}
