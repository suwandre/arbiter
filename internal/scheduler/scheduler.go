package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/models"
	"github.com/suwandre/arbiter/internal/scorer"
)

type Scheduler struct {
	scorer   *scorer.Scorer
	pairs    []string
	interval time.Duration
	cache    map[string][]*models.RawExchangeData // pair -> raw data per exchange
	mu       sync.RWMutex
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewScheduler(scorer *scorer.Scorer, pairs []string, interval time.Duration) *Scheduler {
	return &Scheduler{
		scorer:   scorer,
		pairs:    pairs,
		interval: interval,
		cache:    make(map[string][]*models.RawExchangeData),
	}
}

func (s *Scheduler) Start(parentCtx context.Context) {
	ctx, cancel := context.WithCancel(parentCtx)
	s.cancel = cancel

	s.refresh(ctx)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.refresh(ctx)
			case <-ctx.Done():
				log.Info().Msg("scheduler stopped")
				return
			}
		}
	}()

	log.Info().
		Stringer("interval", s.interval).
		Strs("pairs", s.pairs).
		Msg("scheduler started")
}

func (s *Scheduler) Stop() {
	s.cancel()
	s.wg.Wait()
}

// GetScores returns scored results for a pair, side, and position size.
// Scores are derived from cached raw data — no API calls.
// side: "general", "long", or "short".
// positionSize: position size in USDT. Uses DefaultPosition if <= 0.
func (s *Scheduler) GetScores(pair string, side string, positionSize float64) ([]*models.ExchangeScore, bool) {
	s.mu.RLock()
	rawData, ok := s.cache[pair]
	s.mu.RUnlock()

	if !ok || len(rawData) == 0 {
		return nil, false
	}

	scores, err := s.scorer.ScoreAll(rawData, positionSize, side)
	if err != nil {
		log.Error().Err(err).Str("pair", pair).Str("side", side).Msg("scoring failed")
		return nil, false
	}

	return scores, true
}

// GetRawData returns the cached raw exchange data for a pair.
func (s *Scheduler) GetRawData(pair string) ([]*models.RawExchangeData, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, ok := s.cache[pair]
	return data, ok
}

// refresh fetches raw data for all pairs and updates the cache.
// Only makes exchange API calls — no scoring happens here.
func (s *Scheduler) refresh(ctx context.Context) {
	for _, pair := range s.pairs {
		rawData, err := s.scorer.FetchAll(ctx, pair)
		if err != nil {
			log.Error().Err(err).Str("pair", pair).Msg("scheduler refresh failed")
			continue
		}

		s.mu.Lock()
		s.cache[pair] = rawData
		s.mu.Unlock()

		log.Info().Str("pair", pair).Int("exchanges", len(rawData)).Msg("cache refreshed")

		// Log scores for general side for observability
		scores, err := s.scorer.ScoreAll(rawData, 0, "general")
		if err != nil {
			log.Error().Err(err).Str("pair", pair).Msg("failed to score for logging")
			continue
		}

		for _, score := range scores {
			log.Info().
				Str("pair", pair).
				Str("exchange", score.Exchange).
				Float64("composite", score.CompositeScore).
				Float64("volume_score", score.VolumeScore).
				Float64("oi_score", score.OIScore).
				Float64("slippage_pct", score.SlippagePct).
				Float64("slippage_score", score.SlippageScore).
				Float64("spread_pct", score.SpreadPct).
				Float64("funding_rate", score.FundingRate).
				Float64("raw_volume_24h", score.Volume24h).
				Float64("raw_open_interest", score.OpenInterest).
				Msg("exchange score")
		}
	}
}
