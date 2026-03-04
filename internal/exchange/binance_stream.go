package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/models"
)

func (b *BinanceAdapter) StreamOrderBook(ctx context.Context, pair string, out chan<- *models.OrderBookDepth) error {
	url := fmt.Sprintf("wss://fstream.binance.com/ws/%s@depth@100ms", toLower(pair))

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("binance StreamOrderBook: dial failed: %w", err)
	}
	defer conn.Close()

	log.Info().Str("exchange", "binance").Str("pair", pair).Msg("order book stream connected")

	snapCtx, snapCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer snapCancel()
	snapshot, err := b.GetOrderBookDepth(snapCtx, pair)
	if err != nil {
		return fmt.Errorf("binance StreamOrderBook: initial snapshot failed: %w", err)
	}

	bids := levelMapFromSlice(snapshot.Bids)
	asks := levelMapFromSlice(snapshot.Asks)

	// If snapshot came back empty, log a warning but continue —
	// the book will build up from diffs
	if len(bids) == 0 || len(asks) == 0 {
		log.Warn().Str("exchange", "binance").Str("pair", pair).Msg("order book snapshot empty, book will build from diffs")
	}

	type depthEvent struct {
		LastUpdateID int64      `json:"lastUpdateId"`
		Bids         [][]string `json:"b"`
		Asks         [][]string `json:"a"`
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("binance StreamOrderBook: read error: %w", err)
		}

		var event depthEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			log.Warn().Str("exchange", "binance").Msg("failed to parse depth event, skipping")
			continue
		}

		log.Debug().Str("exchange", "binance").Str("pair", pair).Int("bids", len(event.Bids)).Int("asks", len(event.Asks)).Msg("depth event received")

		// Apply diffs — quantity of 0 means remove the level
		applyDiffs(bids, event.Bids)
		applyDiffs(asks, event.Asks)

		trimBook(bids, 1000, true)
		trimBook(asks, 1000, false)
		depth := buildDepth("binance", pair, bids, asks)

		log.Debug().Str("exchange", "binance").Str("pair", pair).Float64("bidDepth", depth.BidDepth).Float64("askDepth", depth.AskDepth).Msg("depth built")

		select {
		case out <- depth:
		case <-ctx.Done():
			return nil
		}
	}
}

func (b *BinanceAdapter) StreamTicker(ctx context.Context, pair string, out chan<- models.Spread) error {
	url := fmt.Sprintf("wss://fstream.binance.com/ws/%s@bookTicker", toLower(pair))

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("binance StreamTicker: dial failed: %w", err)
	}
	defer conn.Close()

	log.Info().Str("exchange", "binance").Str("pair", pair).Msg("ticker stream connected")

	type tickerEvent struct {
		Symbol   string `json:"s"`
		BidPrice string `json:"b"` // Binance: b = best bid price
		BidQty   string `json:"B"` // Binance: B = best bid quantity
		AskPrice string `json:"a"` // Binance: a = best ask price
		AskQty   string `json:"A"` // Binance: A = best ask quantity
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("binance StreamTicker: read error: %w", err)
		}

		var event tickerEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			log.Debug().Str("exchange", "binance").Str("pair", pair).Msg("failed to parse ticker event, skipping")
			continue
		}

		// Binance bookTicker WebSocket doesn't have an 'e' (eventType) field,
		// so we process all messages that have valid bid/ask prices
		bid, _ := strconv.ParseFloat(event.BidPrice, 64)
		ask, _ := strconv.ParseFloat(event.AskPrice, 64)

		if bid <= 0 || ask <= 0 || ask < bid {
			log.Debug().Str("exchange", "binance").Str("pair", pair).Float64("bid", bid).Float64("ask", ask).Msg("invalid bid/ask values, skipping")
			continue
		}

		select {
		case out <- models.Spread{
			Exchange: "binance",
			Pair:     pair,
			Bid:      bid,
			Ask:      ask,
			Spread:   ask - bid,
		}:
		case <-ctx.Done():
			return nil
		}
	}
}
