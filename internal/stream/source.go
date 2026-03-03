package stream

import "github.com/suwandre/arbiter/internal/models"

// DataSource is implemented by any component that provides cached exchange data.
// Both stream.Manager and scheduler.Scheduler satisfy this interface.
type DataSource interface {
	GetRawData(pair string) ([]*models.RawExchangeData, bool)
}
