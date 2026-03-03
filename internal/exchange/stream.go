package exchange

import (
	"context"

	"github.com/suwandre/arbiter/internal/models"
)

// StreamingExchange is implemented by adapters that support WebSocket streams.
// It embeds Exchange so all REST methods remain available as fallback.
type StreamingExchange interface {
	Exchange

	// StreamOrderBook opens a WebSocket stream for order book updates.
	// Sends *models.OrderBookDepth to out on every meaningful update.
	// Blocks until ctx is cancelled or a fatal error occurs.
	StreamOrderBook(ctx context.Context, pair string, out chan<- *models.OrderBookDepth) error

	// StreamTicker opens a WebSocket stream for best bid/ask updates.
	// Sends models.Spread to out on every update.
	// Blocks until ctx is cancelled or a fatal error occurs.
	StreamTicker(ctx context.Context, pair string, out chan<- models.Spread) error
}
