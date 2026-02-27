package exchange

import (
	"context"

	"github.com/suwandre/arbiter/internal/models"
)

type Exchange interface {
	GetFundingRate(ctx context.Context, pair string) (*models.FundingRate, error)
	GetSpread(ctx context.Context, pair string) (*models.Spread, error)
	GetOrderBookDepth(ctx context.Context, pair string) (*models.OrderBookDepth, error)
	Name() string
}
