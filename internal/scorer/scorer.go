package scorer

import (
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
func (s *Scorer) ScoreAll(pair string) ([]*models.ExchangeScore, error) {
	results := make(chan ExchangeResult, len(s.exchanges))

	var wg sync.WaitGroup

	for _, ex := range s.exchanges {
		wg.Add(1)

		go func(ex exchange.Exchange) {
			defer wg.Done()

			score, err := fetchAndScore(ex, pair)
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

	rankScores(scores)
	return scores, nil
}

// Calls all three data endpoints for one exchange and computes its score.
func fetchAndScore(ex exchange.Exchange, pair string) (*models.ExchangeScore, error) {
	funding, err := ex.GetFundingRate(pair)
	if err != nil {
		return nil, fmt.Errorf("[%s] funding rate error: %w", ex.Name(), err)
	}

	spread, err := ex.GetSpread(pair)
	if err != nil {
		return nil, fmt.Errorf("[%s] spread error: %w", ex.Name(), err)
	}

	depth, err := ex.GetOrderBookDepth(pair)
	if err != nil {
		return nil, fmt.Errorf("[%s] depth error: %w", ex.Name(), err)
	}

	spreadPct := 0.0
	if spread.Bid > 0 {
		spreadPct = (spread.Spread / spread.Bid) * 100
	}

	depthScore := depth.BidDepth + depth.AskDepth

	// Lower funding rate = better, lower spread = better, higher depth = better
	// Composite: we invert funding and spread so higher score = better exchange
	composite := (1/(1+funding.Rate*100))*0.4 +
		(1/(1+spreadPct))*0.4 +
		(depthScore/1_000_000)*0.2

	return &models.ExchangeScore{
		Exchange:       ex.Name(),
		Pair:           pair,
		FundingRate:    funding.Rate,
		SpreadPct:      spreadPct,
		DepthScore:     depthScore,
		CompositeScore: composite,
		UpdatedAt:      time.Now(),
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
