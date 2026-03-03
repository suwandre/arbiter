package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

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

	// Seed with a REST snapshot first so we have a full book before applying diffs
	snapshot, err := b.GetOrderBookDepth(ctx, pair)
	if err != nil {
		return fmt.Errorf("binance StreamOrderBook: initial snapshot failed: %w", err)
	}

	// local book state
	bids := levelMapFromSlice(snapshot.Bids)
	asks := levelMapFromSlice(snapshot.Asks)

	type depthEvent struct {
		EventType string     `json:"e"`
		Bids      [][]string `json:"b"`
		Asks      [][]string `json:"a"`
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

		// Skip non-depth messages (subscription acks, heartbeats)
		if event.EventType != "depthUpdate" {
			continue
		}

		// Apply diffs — quantity of 0 means remove the level
		applyDiffs(bids, event.Bids)
		applyDiffs(asks, event.Asks)

		trimBook(bids, 1000, true)
		trimBook(asks, 1000, false)
		depth := buildDepth("binance", pair, bids, asks)
		select {
		case out <- depth:
		default:
			// Drop if consumer is slow — always prefer fresh data
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
		BidPrice string `json:"b"`
		AskPrice string `json:"a"`
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
			continue
		}

		bid, _ := strconv.ParseFloat(event.BidPrice, 64)
		ask, _ := strconv.ParseFloat(event.AskPrice, 64)

		select {
		case out <- models.Spread{
			Exchange: "binance",
			Pair:     pair,
			Bid:      bid,
			Ask:      ask,
			Spread:   ask - bid,
		}:
		default:
		}
	}
}
