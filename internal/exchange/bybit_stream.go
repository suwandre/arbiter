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

func (b *BybitAdapter) StreamOrderBook(ctx context.Context, pair string, out chan<- *models.OrderBookDepth) error {
	const url = "wss://stream.bybit.com/v5/public/linear"

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("bybit StreamOrderBook: dial failed: %w", err)
	}
	defer conn.Close()

	// Subscribe to order book depth
	sub, _ := json.Marshal(map[string]any{
		"op":   "subscribe",
		"args": []string{fmt.Sprintf("orderbook.1000.%s", pair)},
	})
	if err := conn.WriteMessage(websocket.TextMessage, sub); err != nil {
		return fmt.Errorf("bybit StreamOrderBook: subscribe failed: %w", err)
	}

	log.Info().Str("exchange", "bybit").Str("pair", pair).Msg("order book stream connected")

	type orderBookData struct {
		Bids [][]string `json:"b"` // was "bids"
		Asks [][]string `json:"a"` // was "asks"
	}
	type orderBookMsg struct {
		Topic string        `json:"topic"`
		Type  string        `json:"type"` // "snapshot" or "delta"
		Data  orderBookData `json:"data"`
	}

	bids := make(map[float64]float64)
	asks := make(map[float64]float64)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("bybit StreamOrderBook: read error: %w", err)
		}

		var event orderBookMsg
		if err := json.Unmarshal(msg, &event); err != nil || event.Topic == "" {
			continue // ping/pong or subscription confirmation
		}

		if event.Type == "snapshot" {
			// Full snapshot — replace local book entirely
			bids = make(map[float64]float64)
			asks = make(map[float64]float64)
		}

		applyDiffs(bids, event.Data.Bids)
		applyDiffs(asks, event.Data.Asks)

		trimBook(bids, 1000, true)
		trimBook(asks, 1000, false)
		depth := buildDepth("bybit", pair, bids, asks)
		select {
		case out <- depth:
		default:
		}
	}
}

func (b *BybitAdapter) StreamTicker(ctx context.Context, pair string, out chan<- models.Spread) error {
	const url = "wss://stream.bybit.com/v5/public/linear"

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("bybit StreamTicker: dial failed: %w", err)
	}
	defer conn.Close()

	sub, _ := json.Marshal(map[string]any{
		"op":   "subscribe",
		"args": []string{fmt.Sprintf("tickers.%s", pair)},
	})
	if err := conn.WriteMessage(websocket.TextMessage, sub); err != nil {
		return fmt.Errorf("bybit StreamTicker: subscribe failed: %w", err)
	}

	log.Info().Str("exchange", "bybit").Str("pair", pair).Msg("ticker stream connected")

	type tickerData struct {
		Bid1Price string `json:"bid1Price"`
		Ask1Price string `json:"ask1Price"`
	}
	type tickerMsg struct {
		Topic string     `json:"topic"`
		Data  tickerData `json:"data"`
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("bybit StreamTicker: read error: %w", err)
		}

		var event tickerMsg
		if err := json.Unmarshal(msg, &event); err != nil || event.Topic == "" {
			continue
		}

		bid, _ := strconv.ParseFloat(event.Data.Bid1Price, 64)
		ask, _ := strconv.ParseFloat(event.Data.Ask1Price, 64)
		if bid == 0 || ask == 0 {
			continue // partial ticker update — skip
		}

		select {
		case out <- models.Spread{
			Exchange: "bybit",
			Pair:     pair,
			Bid:      bid,
			Ask:      ask,
			Spread:   ask - bid,
		}:
		default:
		}
	}
}
