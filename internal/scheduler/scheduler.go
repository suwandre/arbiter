package scheduler

import (
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
	stopCh   chan struct{}
}

func NewScheduler(scorer *scorer.Scorer, pairs []string, interval time.Duration) *Scheduler {
	return &Scheduler{
		scorer:   scorer,
		pairs:    pairs,
		interval: interval,
		cache:    make(map[string][]*models.ExchangeScore),
		stopCh:   make(chan struct{}),
	}
}

// Begins the polling loop in a background goroutine.
func (s *Scheduler) Start() {
	// Run once immediately so cache isn't empty on first request
	s.refresh()

	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.refresh()
			case <-s.stopCh:
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
	close(s.stopCh)
}

// Returns the latest cached scores for a pair.
func (s *Scheduler) GetScores(pair string) ([]*models.ExchangeScore, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	scores, ok := s.cache[pair]
	return scores, ok
}

// Fetches fresh scores for all pairs and updates the cache.
func (s *Scheduler) refresh() {
	for _, pair := range s.pairs {
		scores, err := s.scorer.ScoreAll(pair)
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
