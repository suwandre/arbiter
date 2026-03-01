package exchange

import (
	"context"

	"github.com/suwandre/arbiter/internal/models"
)

type Exchange interface {
	Name() string
	GetFundingRate(ctx context.Context, pair string) (*models.FundingRate, error)
	GetSpread(ctx context.Context, pair string) (*models.Spread, error)
	GetOrderBookDepth(ctx context.Context, pair string) (*models.OrderBookDepth, error)
	GetMarketStats(ctx context.Context, pair string) (*models.MarketStats, error)
}
