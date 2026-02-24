package exchange

import "github.com/suwandre/arbiter/internal/models"

type Exchange interface {
	GetFundingRate(pair string) (*models.FundingRate, error)
	GetSpread(pair string) (*models.Spread, error)
	GetOrderBookDepth(pair string) (*models.OrderBookDepth, error)
	Name() string
}
