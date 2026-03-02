package handlers

import (
	"math"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/constants"
	"github.com/suwandre/arbiter/internal/models"
	"github.com/suwandre/arbiter/internal/scheduler"
	"github.com/suwandre/arbiter/internal/scorer"
)

type FundingResult struct {
	Exchange             string  `json:"exchange"`
	CurrentRate          float64 `json:"current_rate"`
	AvgRate30d           float64 `json:"avg_rate_30d"`
	StdDev30d            float64 `json:"std_dev_30d"`
	MinRate30d           float64 `json:"min_rate_30d"`
	MaxRate30d           float64 `json:"max_rate_30d"`
	HistoricalPeriods    int     `json:"historical_periods"`
	ProjectedCostPct     float64 `json:"projected_cost_pct"`      // based on 30d avg
	ProjectedCostLowPct  float64 `json:"projected_cost_low_pct"`  // optimistic: avg - 1 std dev
	ProjectedCostHighPct float64 `json:"projected_cost_high_pct"` // pessimistic: avg + 1 std dev
	Paying               bool    `json:"paying"`                  // true if net cost (not receiving)
}

type FundingHandler struct {
	scheduler *scheduler.Scheduler
}

func NewFundingHandler(scheduler *scheduler.Scheduler) *FundingHandler {
	return &FundingHandler{scheduler}
}

// Handles GET /v1/funding/:pair?side=long|short&hours=72
func (h *FundingHandler) GetFundingCost(c fiber.Ctx) error {
	pair := c.Params("pair")
	if pair == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "pair parameter is required",
		})
	}

	side := c.Query("side", "long")
	if side != "long" && side != "short" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "side must be one of: long, short",
		})
	}

	hours := 24.0 // default 24h
	if raw := c.Query("hours", ""); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed <= 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "hours must be a positive number",
			})
		}
		hours = parsed
	}

	log.Info().
		Str("pair", pair).
		Str("side", side).
		Float64("hours", hours).
		Msg("fetching funding cost")

	rawData, ok := h.scheduler.GetRawData(pair)
	if !ok || len(rawData) == 0 {
		log.Warn().Str("pair", pair).Msg("pair not found in cache")
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "pair not available, check configured pairs",
		})
	}

	// Round up — partial periods still incur a full funding payment
	fundingPeriods := math.Ceil(hours / constants.FundingIntervalHours)

	results := make([]FundingResult, 0, len(rawData))
	for _, raw := range rawData {
		summary := scorer.ComputeFundingSummary(raw)

		// For longs: positive funding = cost, negative = receive
		// For shorts: flip the sign
		sign := 1.0
		if side == "short" {
			sign = -1.0
		}

		projectedCost := sign * summary.AvgRate30d * fundingPeriods * 100
		projectedLow := sign * (summary.AvgRate30d - summary.StdDev30d) * fundingPeriods * 100
		projectedHigh := sign * (summary.AvgRate30d + summary.StdDev30d) * fundingPeriods * 100

		// After flipping sign for shorts, low/high may be inverted — normalize so low <= high always
		if projectedLow > projectedHigh {
			projectedLow, projectedHigh = projectedHigh, projectedLow
		}

		results = append(results, FundingResult{
			Exchange:             raw.Exchange,
			CurrentRate:          summary.CurrentRate,
			AvgRate30d:           summary.AvgRate30d,
			StdDev30d:            summary.StdDev30d,
			MinRate30d:           summary.MinRate30d,
			MaxRate30d:           summary.MaxRate30d,
			HistoricalPeriods:    summary.Periods,
			ProjectedCostPct:     projectedCost,
			ProjectedCostLowPct:  projectedLow,
			ProjectedCostHighPct: projectedHigh,
			Paying:               projectedCost > 0,
		})
	}

	// Sort by projected cost ascending — cheapest (or most received) first
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].ProjectedCostPct < results[i].ProjectedCostPct {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"pair":            pair,
		"side":            side,
		"hold_hours":      hours,
		"funding_periods": fundingPeriods,
		"note":            "projected costs use 30-day average funding rate. low/high range represents ±1 standard deviation.",
		"results":         results,
	})
}

// Handles GET /v1/funding/:pair/arb
// Returns all cross-exchange funding arb opportunities ranked by differential (highest first).
// Only directional pairs where net positive funding can be captured are returned.
func (h *FundingHandler) GetFundingArb(c fiber.Ctx) error {
	pair := c.Params("pair")
	if pair == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "pair parameter is required",
		})
	}

	log.Info().Str("pair", pair).Msg("fetching funding arb opportunities")

	rawData, ok := h.scheduler.GetRawData(pair)
	if !ok || len(rawData) == 0 {
		log.Warn().Str("pair", pair).Msg("pair not found in cache")
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "pair not available, check configured pairs",
		})
	}

	arbs := scorer.ComputeFundingArb(rawData)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"pair":          pair,
		"note":          "differential_pct is the gross funding captured per 8h period. annualized_pct assumes 3 periods/day and does not account for execution costs or basis risk.",
		"count":         len(arbs),
		"opportunities": arbs,
	})
}

// Handles GET /v1/funding/:pair/diff
// Returns the current funding rate for each exchange sorted ascending.
// The top entry is the best long candidate; the bottom is the best short candidate.
func (h *FundingHandler) GetFundingDiff(c fiber.Ctx) error {
	pair := c.Params("pair")
	if pair == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "pair parameter is required",
		})
	}

	log.Info().Str("pair", pair).Msg("fetching funding rate diff")

	rawData, ok := h.scheduler.GetRawData(pair)
	if !ok || len(rawData) == 0 {
		log.Warn().Str("pair", pair).Msg("pair not found in cache")
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "pair not available, check configured pairs",
		})
	}

	type rateEntry struct {
		Exchange   string  `json:"exchange"`
		Rate       float64 `json:"rate"`
		RatePct    float64 `json:"rate_pct"`
		Annualized float64 `json:"annualized_pct"`
	}

	entries := make([]rateEntry, 0, len(rawData))
	for _, raw := range rawData {
		entries = append(entries, rateEntry{
			Exchange:   raw.Exchange,
			Rate:       raw.Funding.Rate,
			RatePct:    raw.Funding.Rate * 100,
			Annualized: raw.Funding.Rate * 100 * constants.FundingPeriodsPerYear,
		})
	}

	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].Rate < entries[i].Rate {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	maxSpread := 0.0
	if len(entries) >= 2 {
		maxSpread = (entries[len(entries)-1].Rate - entries[0].Rate) * 100
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"pair":           pair,
		"note":           "sorted by rate ascending. top = best long candidate, bottom = best short candidate.",
		"max_spread_pct": maxSpread,
		"rates":          entries,
	})
}

// Handles GET /v1/funding/:pair/basis
// Returns the spot/perp basis for each exchange, sorted by absolute basis descending.
// A positive basis means perp is trading at a premium to spot (contango).
// A negative basis means perp is at a discount (backwardation).
func (h *FundingHandler) GetBasis(c fiber.Ctx) error {
	pair := c.Params("pair")
	if pair == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "pair parameter is required",
		})
	}

	log.Info().Str("pair", pair).Msg("fetching spot/perp basis")

	rawData, ok := h.scheduler.GetRawData(pair)
	if !ok || len(rawData) == 0 {
		log.Warn().Str("pair", pair).Msg("pair not found in cache")
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "pair not available, check configured pairs",
		})
	}

	results := scorer.ComputeSpotPerpBasis(rawData)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"pair":    pair,
		"note":    "basis_pct = (perp_mid - spot_mid) / spot_mid * 100. positive = perp premium (contango), negative = perp discount (backwardation). annualized_pct is a proxy and assumes basis is constant.",
		"count":   len(results),
		"results": results,
	})
}

// Handles GET /v1/funding/:pair/cross-basis
// Returns all cross-exchange basis trade opportunities ranked by net basis descending.
// Each entry represents: buy spot on spot_exchange, short perp on perp_exchange.
// net_basis_pct is gross basis minus half-spread entry costs on both legs.
func (h *FundingHandler) GetCrossBasis(c fiber.Ctx) error {
	pair := c.Params("pair")
	if pair == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "pair parameter is required",
		})
	}

	// Optional per-exchange fee overrides via query params:
	// ?binance_spot_fee=0.08&binance_perp_fee=0.04&bybit_spot_fee=0.10 etc.
	feeOverrides := map[string]models.ExchangeFees{}
	for _, name := range []string{"binance", "bybit", "mexc"} {
		spotKey := name + "_spot_fee"
		perpKey := name + "_perp_fee"
		defaults := constants.DefaultTakerFees[name]

		spotFee := defaults.SpotTakerPct
		perpFee := defaults.PerpTakerPct

		if raw := c.Query(spotKey, ""); raw != "" {
			if v, err := strconv.ParseFloat(raw, 64); err == nil && v >= 0 {
				spotFee = v
			}
		}
		if raw := c.Query(perpKey, ""); raw != "" {
			if v, err := strconv.ParseFloat(raw, 64); err == nil && v >= 0 {
				perpFee = v
			}
		}

		feeOverrides[name] = models.ExchangeFees{
			SpotTakerPct: spotFee,
			PerpTakerPct: perpFee,
		}
	}

	log.Info().Str("pair", pair).Msg("fetching cross-exchange basis opportunities")

	rawData, ok := h.scheduler.GetRawData(pair)
	if !ok || len(rawData) == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "pair not available, check configured pairs",
		})
	}

	opportunities := scorer.ComputeCrossBasisOpportunities(rawData, feeOverrides)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"pair":          pair,
		"note":          "buy spot on spot_exchange, short perp on perp_exchange. net_basis_pct = gross basis minus taker fees on both legs. override fees via query params: ?binance_spot_fee=0.08&mexc_perp_fee=0.00. annualized estimates use 30d avg funding rate on the perp leg ± 1 stddev.",
		"fees_used":     feeOverrides,
		"count":         len(opportunities),
		"opportunities": opportunities,
	})
}
