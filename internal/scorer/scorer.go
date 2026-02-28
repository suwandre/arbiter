package scorer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/exchange"
	"github.com/suwandre/arbiter/internal/models"
)

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

	// Now compute composite with normalized depth
	for _, score := range scores {
		score.CompositeScore = (1/(1+score.FundingRate*100))*0.4 +
			(1/(1+score.SpreadPct))*0.4 +
			score.DepthScore*0.2
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

	spreadPct := 0.0
	if spread.Bid > 0 {
		spreadPct = (spread.Spread / spread.Bid) * 100
	}

	return &models.ExchangeScore{
		Exchange:    ex.Name(),
		Pair:        pair,
		FundingRate: funding.Rate,
		SpreadPct:   spreadPct,
		RawBidDepth: depth.BidDepth,                  // raw bid depth
		RawAskDepth: depth.AskDepth,                  // raw ask depth
		DepthScore:  depth.BidDepth + depth.AskDepth, // raw depth score (sum of bid and ask depth), normalized later in ScoreALl
		UpdatedAt:   time.Now(),
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
