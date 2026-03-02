package constants

import "github.com/suwandre/arbiter/internal/models"

const (
	// FundingIntervalHours is the standard funding interval used by all supported exchanges.
	FundingIntervalHours = 8.0

	// FundingPeriodsPerDay is the number of funding settlements per day.
	FundingPeriodsPerDay = 24.0 / FundingIntervalHours

	// FundingPeriodsPerYear is used for annualizing funding rate differentials.
	FundingPeriodsPerYear = FundingPeriodsPerDay * 365.0

	// DefaultPositionUSDT is the fallback position size used when none is provided.
	DefaultPositionUSDT = 500_000.0

	// OrderBookTargetLevels is the number of order book levels to request per side.
	OrderBookTargetLevels = 20
)

// TakerFee holds the default taker fee percentages per exchange (as a percent, e.g. 0.05 = 0.05%).
// These are standard tier fees — users with VIP tiers can override via query params.
// Spot fees are used for the spot leg of basis trades.
// Perp fees are used for the perp leg.
var DefaultTakerFees = map[string]models.ExchangeFees{
	"binance": {SpotTakerPct: 0.10, PerpTakerPct: 0.05},
	"bybit":   {SpotTakerPct: 0.10, PerpTakerPct: 0.055},
	"mexc":    {SpotTakerPct: 0.00, PerpTakerPct: 0.00},
}
