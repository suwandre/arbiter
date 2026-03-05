package stream

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/exchange"
	"github.com/suwandre/arbiter/internal/models"
)

const (
	restPollInterval  = 30 * time.Second // funding, stats, spot price
	reconnectBaseWait = 1 * time.Second  // initial reconnect delay
	reconnectMaxWait  = 60 * time.Second // cap on reconnect backoff
)

// Manager maintains live RawExchangeData per pair+exchange using WS streams
// for order book and ticker, and periodic REST calls for funding/stats/spot.
type Manager struct {
	exchanges []exchange.StreamingExchange
	pairs     []string
	state     map[string]map[string]*models.RawExchangeData
	mu        sync.RWMutex
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	startedAt time.Time       // set once in Start()
	wsStatus  map[string]bool // exchange name -> WS connected
	wsMu      sync.RWMutex
}

// PairStatus holds cache freshness info for a single pair+exchange.
type PairStatus struct {
	Exchange  string    `json:"exchange"`
	UpdatedAt time.Time `json:"updated_at"`
	Stale     bool      `json:"stale"` // true if not updated in the last 60s
}

// ExchangeStatus holds WS connection state for a single exchange.
type ExchangeStatus struct {
	Exchange    string `json:"exchange"`
	WSConnected bool   `json:"ws_connected"`
}

func NewManager(exchanges []exchange.StreamingExchange, pairs []string) *Manager {
	state := make(map[string]map[string]*models.RawExchangeData)
	for _, pair := range pairs {
		state[pair] = make(map[string]*models.RawExchangeData)
		for _, ex := range exchanges {
			state[pair][ex.Name()] = &models.RawExchangeData{
				Exchange: ex.Name(),
				Pair:     pair,
				Depth:    &models.OrderBookDepth{},
			}
		}
	}
	return &Manager{
		exchanges: exchanges,
		pairs:     pairs,
		state:     state,
		wsStatus:  make(map[string]bool),
	}
}

// Start launches all WS streams and REST pollers.
func (m *Manager) Start(parentCtx context.Context) {
	ctx, cancel := context.WithCancel(parentCtx)
	m.cancel = cancel
	m.startedAt = time.Now()

	// initialize all WS statuses to false before streams come up
	m.wsMu.Lock()
	for _, ex := range m.exchanges {
		m.wsStatus[ex.Name()] = false
	}
	m.wsMu.Unlock()

	// Seed all exchange+pair REST data concurrently so slow exchanges
	// (e.g. MEXC) don't block faster ones during startup.
	var seedWg sync.WaitGroup
	for _, ex := range m.exchanges {
		for _, pair := range m.pairs {
			seedWg.Add(1)
			go func(ex exchange.StreamingExchange, pair string) {
				defer seedWg.Done()
				m.fetchREST(ctx, ex, pair)
			}(ex, pair)
		}
	}
	seedWg.Wait()

	for _, ex := range m.exchanges {
		for _, pair := range m.pairs {
			m.wg.Add(1)
			go m.runOrderBookStream(ctx, ex, pair)

			m.wg.Add(1)
			go m.runTickerStream(ctx, ex, pair)

			m.wg.Add(1)
			go m.runRESTPoller(ctx, ex, pair)
		}
	}

	log.Info().
		Strs("pairs", m.pairs).
		Int("exchanges", len(m.exchanges)).
		Msg("stream manager started")
}

func (m *Manager) Stop() {
	m.cancel()
	m.wg.Wait()
	log.Info().Msg("stream manager stopped")
}

// GetRawData returns a snapshot of the current live state for a pair.
func (m *Manager) GetRawData(pair string) ([]*models.RawExchangeData, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	perExchange, ok := m.state[pair]
	if !ok || len(perExchange) == 0 {
		return nil, false
	}

	result := make([]*models.RawExchangeData, 0, len(perExchange))
	for _, data := range perExchange {
		// Shallow copy to avoid callers mutating live state
		copy := *data
		if data.Depth != nil {
			depthCopy := *data.Depth
			copy.Depth = &depthCopy
		}
		result = append(result, &copy)
	}
	return result, true
}

// Pairs returns all configured pairs and their per-exchange cache freshness.
func (m *Manager) Pairs() map[string][]PairStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string][]PairStatus, len(m.pairs))
	for _, pair := range m.pairs {
		statuses := make([]PairStatus, 0)
		for exName, data := range m.state[pair] {
			stale := time.Since(data.FetchedAt) > 60*time.Second
			statuses = append(statuses, PairStatus{
				Exchange:  exName,
				UpdatedAt: data.FetchedAt,
				Stale:     stale,
			})
		}
		out[pair] = statuses
	}
	return out
}

// Status returns WS connection state per exchange and server uptime.
func (m *Manager) Status() ([]ExchangeStatus, time.Duration) {
	m.wsMu.RLock()
	defer m.wsMu.RUnlock()

	statuses := make([]ExchangeStatus, 0, len(m.exchanges))
	for _, ex := range m.exchanges {
		statuses = append(statuses, ExchangeStatus{
			Exchange:    ex.Name(),
			WSConnected: m.wsStatus[ex.Name()],
		})
	}
	return statuses, time.Since(m.startedAt)
}

// runOrderBookStream runs StreamOrderBook with exponential backoff reconnect.
func (m *Manager) runOrderBookStream(ctx context.Context, ex exchange.StreamingExchange, pair string) {
	defer m.wg.Done()

	out := make(chan *models.OrderBookDepth, 10)
	wait := reconnectBaseWait

	for {
		// Drain channel before reconnect
		for len(out) > 0 {
			<-out
		}

		// Launch stream in a goroutine so we can select on ctx and out simultaneously
		streamCtx, streamCancel := context.WithCancel(ctx)
		go func() {
			if err := ex.StreamOrderBook(streamCtx, pair, out); err != nil {
				if streamCtx.Err() == nil {
					log.Error().Err(err).
						Str("exchange", ex.Name()).
						Str("pair", pair).
						Msg("order book stream error")
				}
			}
			streamCancel()
		}()

		// Consume updates until stream dies or ctx cancelled
	consume:
		for {
			select {
			case <-ctx.Done():
				streamCancel()
				return
			case <-streamCtx.Done():
				break consume
			case depth, ok := <-out:
				if !ok {
					break consume
				}
				m.mu.Lock()
				m.state[pair][ex.Name()].Depth = depth
				m.state[pair][ex.Name()].FetchedAt = time.Now()
				m.mu.Unlock()

				m.wsMu.Lock()
				m.wsStatus[ex.Name()] = true // stream is alive
				m.wsMu.Unlock()
				wait = reconnectBaseWait
			}
		}

		m.wsMu.Lock()
		m.wsStatus[ex.Name()] = false // stream just died
		m.wsMu.Unlock()

		// Stream died — reconnect with backoff
		log.Warn().
			Str("exchange", ex.Name()).
			Str("pair", pair).
			Dur("wait", wait).
			Msg("order book stream disconnected, reconnecting")

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		wait = min(wait*2, reconnectMaxWait)
	}
}

// runTickerStream runs StreamTicker with exponential backoff reconnect.
func (m *Manager) runTickerStream(ctx context.Context, ex exchange.StreamingExchange, pair string) {
	defer m.wg.Done()

	out := make(chan models.Spread, 10)
	wait := reconnectBaseWait

	for {
		for len(out) > 0 {
			<-out
		}

		streamCtx, streamCancel := context.WithCancel(ctx)
		go func() {
			if err := ex.StreamTicker(streamCtx, pair, out); err != nil {
				if streamCtx.Err() == nil {
					log.Error().Err(err).
						Str("exchange", ex.Name()).
						Str("pair", pair).
						Msg("ticker stream error")
				}
			}
			streamCancel()
		}()

	consume:
		for {
			log.Debug().Str("exchange", ex.Name()).Str("pair", pair).Msg("consume loop tick")
			select {
			case <-ctx.Done():
				streamCancel()
				return
			case <-streamCtx.Done():
				break consume
			case spread, ok := <-out:
				if !ok {
					break consume
				}
				m.mu.Lock()
				m.state[pair][ex.Name()].Spread = spread
				m.mu.Unlock()
				log.Debug().
					Str("exchange", ex.Name()).
					Str("pair", pair).
					Float64("bid", spread.Bid).
					Float64("ask", spread.Ask).
					Msg("ticker state updated")
				wait = reconnectBaseWait
			}
		}

		log.Warn().
			Str("exchange", ex.Name()).
			Str("pair", pair).
			Dur("wait", wait).
			Msg("ticker stream disconnected, reconnecting")

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		wait = min(wait*2, reconnectMaxWait)
	}
}

// runRESTPoller fetches funding, stats, and spot price on a slow timer.
func (m *Manager) runRESTPoller(ctx context.Context, ex exchange.StreamingExchange, pair string) {
	defer m.wg.Done()

	ticker := time.NewTicker(restPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.fetchREST(ctx, ex, pair)
		}
	}
}

// fetchREST fetches funding rate + history, market stats, and spot price via REST.
// Called once at startup and then every restPollInterval.
func (m *Manager) fetchREST(ctx context.Context, ex exchange.StreamingExchange, pair string) {
	// Bound each REST poll cycle so slow exchanges don't block the goroutine.
	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	funding, err := ex.GetFundingRate(fetchCtx, pair)
	if err != nil {
		log.Error().Err(err).Str("exchange", ex.Name()).Str("pair", pair).Msg("REST: funding rate failed")
	}

	history, err := ex.GetFundingRateHistory(fetchCtx, pair, 90)
	if err != nil {
		log.Error().Err(err).Str("exchange", ex.Name()).Str("pair", pair).Msg("REST: funding history failed")
	}

	stats, err := ex.GetMarketStats(fetchCtx, pair)
	if err != nil {
		log.Error().Err(err).Str("exchange", ex.Name()).Str("pair", pair).Msg("REST: market stats failed")
	}

	spot, err := ex.GetSpotPrice(fetchCtx, pair)
	if err != nil {
		log.Error().Err(err).Str("exchange", ex.Name()).Str("pair", pair).Msg("REST: spot price failed")
	}

	m.mu.Lock()
	s := m.state[pair][ex.Name()]
	if funding.Exchange != "" {
		s.Funding = funding
	}
	if len(history) > 0 {
		s.FundingHistory = history
	}
	if stats.Exchange != "" {
		s.Stats = stats
	}
	if spot > 0 {
		s.SpotMidPrice = spot
	}
	s.FetchedAt = time.Now()
	m.mu.Unlock()
}
