package handlers

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/constants"
	"github.com/suwandre/arbiter/internal/scorer"
	"github.com/suwandre/arbiter/internal/stream"
)

type ScoreHandler struct {
	data   stream.DataSource
	scorer *scorer.Scorer
}

func NewScoreHandler(data stream.DataSource, sc *scorer.Scorer) *ScoreHandler {
	return &ScoreHandler{data: data, scorer: sc}
}

// GET /v1/scores/:pair?side=general|long|short&position=500000&mode=entry_long&hours=24
func (h *ScoreHandler) GetScores(c fiber.Ctx) error {
	pair := c.Params("pair")
	if pair == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "pair parameter is required",
		})
	}

	side := c.Query("side", "general")
	if side != "general" && side != "long" && side != "short" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "side must be one of: general, long, short",
		})
	}

	mode, ok := scorer.ParseScoringMode(c.Query("mode", ""))
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid mode, must be one of: " + strings.Join(scorer.ValidScoringModes(), ", "),
		})
	}

	positionSize := 0.0
	if raw := c.Query("position", ""); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed <= 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "position must be a positive number",
			})
		}
		positionSize = parsed
	}

	hours := 0.0
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
		Str("mode", string(mode)).
		Float64("position_size", positionSize).
		Msg("fetching scores")

	rawData, ok := h.data.GetRawData(pair)
	if !ok || len(rawData) == 0 {
		log.Warn().Str("pair", pair).Msg("pair not found in cache")
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "pair not available, check configured pairs",
		})
	}

	scores, err := h.scorer.ScoreAll(rawData, positionSize, side, mode, hours)
	if err != nil {
		log.Error().Err(err).Str("pair", pair).Str("mode", string(mode)).Msg("scoring failed")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "scoring failed",
		})
	}

	displayPosition := positionSize
	if displayPosition == 0 {
		displayPosition = constants.DefaultPositionUSDT
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"pair":          pair,
		"side":          side,
		"mode":          string(mode),
		"position_size": displayPosition,
		"scores":        scores,
	})
}
