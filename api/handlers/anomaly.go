package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/internal/scorer"
	"github.com/suwandre/arbiter/internal/stream"
)

type AnomalyHandler struct {
	data stream.DataSource
}

func NewAnomalyHandler(data stream.DataSource) *AnomalyHandler {
	return &AnomalyHandler{data: data}
}

// GET /v1/anomaly/:pair
func (h *AnomalyHandler) GetAnomaly(c fiber.Ctx) error {
	pair := c.Params("pair")
	if pair == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "pair parameter is required",
		})
	}

	rawData, ok := h.data.GetRawData(pair)
	if !ok || len(rawData) == 0 {
		log.Warn().Str("pair", pair).Msg("pair not found in cache")
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "pair not available, check configured pairs",
		})
	}

	results := scorer.ComputeAnomaly(rawData)

	// count how many exchanges have at least one signal
	flagged := 0
	for _, r := range results {
		if !r.Clean {
			flagged++
		}
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"pair":              pair,
		"exchanges_flagged": flagged,
		"results":           results,
	})
}
