package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/constants"
	"github.com/suwandre/arbiter/internal/scheduler"
)

type ScoreHandler struct {
	scheduler *scheduler.Scheduler
}

func NewScoreHandler(scheduler *scheduler.Scheduler) *ScoreHandler {
	return &ScoreHandler{scheduler}
}

// Handles GET /v1/scores/:pair?side=general|long|short&position=500000
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

	positionSize := 0.0 // 0 = use scorer default
	if raw := c.Query("position", ""); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed <= 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "position must be a positive number",
			})
		}
		positionSize = parsed
	}

	log.Info().
		Str("pair", pair).
		Str("side", side).
		Float64("position_size", positionSize).
		Msg("fetching scores")

	scores, ok := h.scheduler.GetScores(pair, side, positionSize)
	if !ok {
		log.Warn().Str("pair", pair).Str("side", side).Msg("pair not found in cache")
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "pair not available, check configured pairs",
		})
	}

	displayPosition := positionSize
	if displayPosition == 0 {
		displayPosition = constants.DefaultPositionUSDT
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"pair":          pair,
		"side":          side,
		"position_size": displayPosition,
		"scores":        scores,
	})
}
