package stream

import (
	"time"

	"github.com/suwandre/arbiter/internal/models"
)

// DataSource is implemented by any component that provides cached exchange data.
// Both stream.Manager and scheduler.Scheduler satisfy this interface.
type DataSource interface {
	GetRawData(pair string) ([]*models.RawExchangeData, bool)
}

// MonitorSource extends DataSource with operational metadata.
// Implemented by stream.Manager.
type MonitorSource interface {
	DataSource
	Pairs() map[string][]PairStatus
	Status() ([]ExchangeStatus, time.Duration)
}
