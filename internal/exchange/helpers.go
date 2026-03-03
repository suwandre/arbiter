// internal/exchange/helpers.go
package exchange

import (
	"sort"
	"strconv"
	"strings"

	"github.com/suwandre/arbiter/internal/models"
)

// Converts [][]string (price, qty) into OrderBookLevels.
// Used by Binance and Bybit adapters.
func parseLevels(raw [][]string) []models.OrderBookLevel {
	levels := make([]models.OrderBookLevel, 0, len(raw))
	for _, entry := range raw {
		price, _ := strconv.ParseFloat(entry[0], 64)
		qty, _ := strconv.ParseFloat(entry[1], 64)
		levels = append(levels, models.OrderBookLevel{Price: price, Quantity: qty})
	}
	return levels
}

// Private helper — sums total quote value across order book levels.
func sumDepth(levels [][]string) float64 {
	total := 0.0
	for _, level := range levels {
		price, _ := strconv.ParseFloat(level[0], 64)
		qty, _ := strconv.ParseFloat(level[1], 64)
		total += price * qty
	}
	return total
}

// toLower lowercases the pair symbol for WS URL construction.
func toLower(pair string) string {
	return strings.ToLower(pair)
}

// levelMapFromSlice converts a slice of OrderBookLevel into a price-keyed map
// for efficient diff application.
func levelMapFromSlice(levels []models.OrderBookLevel) map[float64]float64 {
	m := make(map[float64]float64, len(levels))
	for _, lvl := range levels {
		m[lvl.Price] = lvl.Quantity
	}
	return m
}

// applyDiffs applies a Binance-style diff update to a local book side.
// Quantity "0" means remove the price level.
func applyDiffs(book map[float64]float64, diffs [][]string) {
	for _, d := range diffs {
		if len(d) < 2 {
			continue
		}
		price, err1 := strconv.ParseFloat(d[0], 64)
		qty, err2 := strconv.ParseFloat(d[1], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		if qty == 0 {
			delete(book, price)
		} else {
			book[price] = qty
		}
	}
}

// buildDepth reconstructs an OrderBookDepth from live bid/ask maps.
// Bids are sorted descending (best bid first), asks ascending (best ask first).
func buildDepth(exchange, pair string, bids, asks map[float64]float64) *models.OrderBookDepth {
	bidLevels := sortedLevels(bids, true)
	askLevels := sortedLevels(asks, false)

	midPrice := 0.0
	if len(bidLevels) > 0 && len(askLevels) > 0 {
		midPrice = (bidLevels[0].Price + askLevels[0].Price) / 2
	}

	return &models.OrderBookDepth{
		Exchange: exchange,
		Pair:     pair,
		BidDepth: sumLevels(bidLevels),
		AskDepth: sumLevels(askLevels),
		Bids:     bidLevels,
		Asks:     askLevels,
		MidPrice: midPrice,
	}
}

// sortedLevels returns book levels sorted by price.
// descending=true for bids (best = highest price first).
// descending=false for asks (best = lowest price first).
func sortedLevels(book map[float64]float64, descending bool) []models.OrderBookLevel {
	levels := make([]models.OrderBookLevel, 0, len(book))
	for price, qty := range book {
		levels = append(levels, models.OrderBookLevel{Price: price, Quantity: qty})
	}
	sort.Slice(levels, func(i, j int) bool {
		if descending {
			return levels[i].Price > levels[j].Price
		}
		return levels[i].Price < levels[j].Price
	})
	return levels
}

func sumLevels(levels []models.OrderBookLevel) float64 {
	total := 0.0
	for _, lvl := range levels {
		total += lvl.Price * lvl.Quantity
	}
	return total
}

// trimBook keeps only the top maxLevels from a book map.
// For bids: keeps highest prices. For asks: keeps lowest prices.
func trimBook(book map[float64]float64, maxLevels int, bids bool) {
	if len(book) <= maxLevels {
		return
	}
	levels := make([]float64, 0, len(book))
	for price := range book {
		levels = append(levels, price)
	}
	sort.Slice(levels, func(i, j int) bool {
		if bids {
			return levels[i] > levels[j] // descending for bids — keep highest
		}
		return levels[i] < levels[j] // ascending for asks — keep lowest
	})
	for _, price := range levels[maxLevels:] {
		delete(book, price)
	}
}
