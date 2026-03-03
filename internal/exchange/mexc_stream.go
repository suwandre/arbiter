package exchange

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/models"
)

func (m *MexcAdapter) StreamOrderBook(ctx context.Context, pair string, out chan<- *models.OrderBookDepth) error {
	const url = "wss://contract.mexc.com/edge"

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("mexc StreamOrderBook: dial failed: %w", err)
	}
	defer conn.Close()

	sub, _ := json.Marshal(map[string]any{
		"method": "sub.depth",
		"param":  map[string]string{"symbol": toMexcSymbol(pair)},
	})
	if err := conn.WriteMessage(websocket.TextMessage, sub); err != nil {
		return fmt.Errorf("mexc StreamOrderBook: subscribe failed: %w", err)
	}

	log.Info().Str("exchange", "mexc").Str("pair", pair).Msg("order book stream connected")

	type depthData struct {
		Bids [][]float64 `json:"bids"` // [price, contracts, order_count]
		Asks [][]float64 `json:"asks"`
	}
	type depthMsg struct {
		Channel string    `json:"channel"`
		Data    depthData `json:"data"`
	}

	contractSize := m.contractSizes[toMexcSymbol(pair)]
	if contractSize == 0 {
		contractSize = 1.0
	}

	// Seed local book from REST snapshot first
	snapshot, err := m.GetOrderBookDepth(ctx, pair)
	if err != nil {
		return fmt.Errorf("mexc StreamOrderBook: initial snapshot failed: %w", err)
	}
	bids := levelMapFromSlice(snapshot.Bids)
	asks := levelMapFromSlice(snapshot.Asks)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("mexc StreamOrderBook: read error: %w", err)
		}

		var event depthMsg
		if err := json.Unmarshal(msg, &event); err != nil || event.Channel == "" {
			continue
		}

		// Apply diffs — quantity 0 = remove level, >0 = update
		for _, lvl := range event.Data.Bids {
			if len(lvl) < 2 {
				continue
			}
			qty := lvl[1] * contractSize // contracts → base asset
			if qty == 0 {
				delete(bids, lvl[0])
			} else {
				bids[lvl[0]] = qty
			}
		}
		for _, lvl := range event.Data.Asks {
			if len(lvl) < 2 {
				continue
			}
			qty := lvl[1] * contractSize
			if qty == 0 {
				delete(asks, lvl[0])
			} else {
				asks[lvl[0]] = qty
			}
		}

		trimBook(bids, 1000, true)
		trimBook(asks, 1000, false)
		depth := buildDepth("mexc", pair, bids, asks)
		select {
		case out <- depth:
		default:
		}
	}
}

func (m *MexcAdapter) StreamTicker(ctx context.Context, pair string, out chan<- models.Spread) error {
	const url = "wss://contract.mexc.com/edge"

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("mexc StreamTicker: dial failed: %w", err)
	}
	defer conn.Close()

	sub, _ := json.Marshal(map[string]any{
		"method": "sub.ticker",
		"param":  map[string]string{"symbol": pair},
	})
	if err := conn.WriteMessage(websocket.TextMessage, sub); err != nil {
		return fmt.Errorf("mexc StreamTicker: subscribe failed: %w", err)
	}

	log.Info().Str("exchange", "mexc").Str("pair", pair).Msg("ticker stream connected")

	type tickerData struct {
		Bid1 float64 `json:"bid1"`
		Ask1 float64 `json:"ask1"`
	}
	type tickerMsg struct {
		Channel string     `json:"channel"`
		Data    tickerData `json:"data"`
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("mexc StreamTicker: read error: %w", err)
		}

		var event tickerMsg
		if err := json.Unmarshal(msg, &event); err != nil || event.Channel == "" {
			continue
		}

		if event.Data.Bid1 == 0 || event.Data.Ask1 == 0 {
			continue
		}

		select {
		case out <- models.Spread{
			Exchange: "mexc",
			Pair:     pair,
			Bid:      event.Data.Bid1,
			Ask:      event.Data.Ask1,
			Spread:   event.Data.Ask1 - event.Data.Bid1,
		}:
		default:
		}
	}
}
