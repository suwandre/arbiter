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
	cache    map[string][]*models.ExchangeScore
	mu       sync.RWMutex
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewScheduler(scorer *scorer.Scorer, pairs []string, interval time.Duration) *Scheduler {
	return &Scheduler{
		scorer:   scorer,
		pairs:    pairs,
		interval: interval,
		cache:    make(map[string][]*models.ExchangeScore),
	}
}

// Begins the polling loop in a background goroutine.
func (s *Scheduler) Start(parentCtx context.Context) {
	ctx, cancel := context.WithCancel(parentCtx)
	s.cancel = cancel

	// Run immediately once so cache isn't empty on start
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

// Signals the background goroutine to exit cleanly.
func (s *Scheduler) Stop() {
	s.cancel()
	// blocks until the goroutine fully exits.
	// if refresh() is running midway when main() exits, Stop() is blocked until the goroutine finishes,
	// ensuring that the HTTP request finishes and the cache is updated before the goroutine exits.
	s.wg.Wait()
}

// Returns the latest cached scores for a pair.
func (s *Scheduler) GetScores(pair string) ([]*models.ExchangeScore, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	scores, ok := s.cache[pair]
	return scores, ok
}

// Fetches fresh scores for all pairs and updates the cache.
func (s *Scheduler) refresh(ctx context.Context) {
	for _, pair := range s.pairs {
		scores, err := s.scorer.ScoreAll(ctx, pair)
		if err != nil {
			log.Error().Err(err).Str("pair", pair).Msg("scheduler refresh failed")
			continue
		}

		s.mu.Lock()
		s.cache[pair] = scores
		s.mu.Unlock()

		log.Info().Str("pair", pair).Int("exchanges", len(scores)).Msg("cache refreshed")
	}
}
