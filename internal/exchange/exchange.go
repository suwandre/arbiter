package exchange

import (
	"context"

	"github.com/suwandre/arbiter/internal/models"
)

type Exchange interface {
	Name() string
	GetFundingRate(ctx context.Context, pair string) (models.FundingRate, error)
	GetFundingRateHistory(ctx context.Context, pair string, limit int) ([]models.FundingRateHistory, error)
	GetSpread(ctx context.Context, pair string) (models.Spread, error)
	GetOrderBookDepth(ctx context.Context, pair string) (*models.OrderBookDepth, error)
	GetMarketStats(ctx context.Context, pair string) (models.MarketStats, error)
	GetSpotPrice(ctx context.Context, pair string) (float64, error)
}
