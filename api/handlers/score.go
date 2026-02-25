package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/scheduler"
)

type ScoreHandler struct {
	scheduler *scheduler.Scheduler
}

func NewScoreHandler(scheduler *scheduler.Scheduler) *ScoreHandler {
	return &ScoreHandler{scheduler}
}

// Handles GET /scores/:pair.
func (h *ScoreHandler) GetScores(c fiber.Ctx) error {
	pair := c.Params("pair")

	if pair == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "pair parameter is required",
		})
	}

	log.Info().
		Str("pair", pair).
		Msg("fetching scores")

	scores, ok := h.scheduler.GetScores(pair)

	if !ok {
		log.Warn().Str("pair", pair).Msg("pair not found in cache")
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "pair not available, check configured pairs",
		})
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"pair":   pair,
		"scores": scores,
	})
}
