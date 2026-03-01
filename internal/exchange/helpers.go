// internal/exchange/helpers.go
package exchange

import (
	"strconv"

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
