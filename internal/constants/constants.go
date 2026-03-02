package constants

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
