package handlers

import (
	"math"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/scheduler"
	"github.com/suwandre/arbiter/internal/scorer"
)

const fundingIntervalHours = 8.0

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
	fundingPeriods := math.Ceil(hours / fundingIntervalHours)

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
