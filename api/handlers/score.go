package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/scorer"
)

type ScoreHandler struct {
	scorer *scorer.Scorer
}

func NewScoreHandler(scorer *scorer.Scorer) *ScoreHandler {
	return &ScoreHandler{scorer: scorer}
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

	scores, err := h.scorer.ScoreAll(pair)
	if err != nil {
		log.Error().
			Err(err).
			Str("pair", pair).
			Msg("failed to score exchanges")

		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"pair":   pair,
		"scores": scores,
	})
}
