package handlers

import (
	"fmt"
	"math"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/constants"
	"github.com/suwandre/arbiter/internal/models"
	"github.com/suwandre/arbiter/internal/scorer"
	"github.com/suwandre/arbiter/internal/stream"
)

type RecommendHandler struct {
	data stream.DataSource
	sc   *scorer.Scorer
}

func NewRecommendHandler(data stream.DataSource, sc *scorer.Scorer) *RecommendHandler {
	return &RecommendHandler{data: data, sc: sc}
}

// GET /v1/recommend/:pair?side=long&size=8000&hours=240
func (h *RecommendHandler) GetRecommendation(c fiber.Ctx) error {
	pair := c.Params("pair")
	if pair == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "pair is required"})
	}

	side := c.Query("side", "long")
	if side != "long" && side != "short" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "side must be long or short"})
	}

	size := 10000.0
	if raw := c.Query("size", ""); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v <= 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "size must be a positive number"})
		}
		size = v
	}

	hours := 24.0
	if raw := c.Query("hours", ""); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v <= 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "hours must be a positive number"})
		}
		hours = v
	}

	log.Info().
		Str("pair", pair).
		Str("side", side).
		Float64("size", size).
		Float64("hours", hours).
		Msg("recommendation requested")

	rawData, ok := h.data.GetRawData(pair)
	if !ok || len(rawData) == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "pair not available"})
	}

	// --- 1. Derive weights and score ---
	rankings, err := h.sc.ScoreAll(rawData, size, side, scorer.ModeGeneral, hours)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// --- 2. Funding cost projection ---
	fundingPeriods := math.Ceil(hours / constants.FundingIntervalHours)
	fundingCosts := make([]models.FundingResult, 0, len(rawData))
	for _, raw := range rawData {
		summary := scorer.ComputeFundingSummary(raw)
		sign := 1.0
		if side == "short" {
			sign = -1.0
		}
		projected := sign * summary.AvgRate30d * fundingPeriods * 100
		low := sign * (summary.AvgRate30d - summary.StdDev30d) * fundingPeriods * 100
		high := sign * (summary.AvgRate30d + summary.StdDev30d) * fundingPeriods * 100
		if low > high {
			low, high = high, low
		}
		fundingCosts = append(fundingCosts, models.FundingResult{
			Exchange:             raw.Exchange,
			CurrentRate:          summary.CurrentRate,
			AvgRate30d:           summary.AvgRate30d,
			StdDev30d:            summary.StdDev30d,
			MinRate30d:           summary.MinRate30d,
			MaxRate30d:           summary.MaxRate30d,
			HistoricalPeriods:    summary.Periods,
			ProjectedCostPct:     projected,
			ProjectedCostLowPct:  low,
			ProjectedCostHighPct: high,
			NetPnlPct:            -projected, // flip sign so positive = gain
			Paying:               projected > 0,
		})
	}

	// Sort funding costs ascending (cheapest first)
	for i := 0; i < len(fundingCosts)-1; i++ {
		for j := i + 1; j < len(fundingCosts); j++ {
			if fundingCosts[j].ProjectedCostPct < fundingCosts[i].ProjectedCostPct {
				fundingCosts[i], fundingCosts[j] = fundingCosts[j], fundingCosts[i]
			}
		}
	}

	// --- 3. Anomaly detection ---
	anomalies := scorer.ComputeAnomaly(rawData)

	// --- 4. Expose derived weights ---
	w := scorer.DeriveWeights(side, hours, size)
	weightsUsed := map[string]float64{
		"volume":    w.Volume,
		"spread":    w.Spread,
		"oi":        w.OI,
		"slippage":  w.Slippage,
		"funding":   w.Funding,
		"bid_depth": w.BidDepth,
	}

	// --- 5. Generate summary ---
	bestExchange := rankings[0]
	bestFunding := fundingCosts[0] // already sorted cheapest first
	summary := fmt.Sprintf(
		"Best for your %.0f-hour $%.0f %s: %s (score %.2f, funding cost %.3f%%)",
		hours,
		size,
		side,
		bestExchange.Exchange,
		bestExchange.CompositeScore,
		bestFunding.ProjectedCostPct,
	)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"request": fiber.Map{
			"pair":          pair,
			"side":          side,
			"position_usdt": size,
			"hold_hours":    hours,
		},
		"summary":       summary, // NEW
		"weights_used":  weightsUsed,
		"rankings":      rankings,
		"funding_costs": fundingCosts,
		"anomalies":     anomalies,
	})
}
