package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/suwandre/arbiter/internal/stream"
)

type MetaHandler struct {
	monitor stream.MonitorSource
}

func NewMetaHandler(monitor stream.MonitorSource) *MetaHandler {
	return &MetaHandler{monitor: monitor}
}

// GET /v1/health
func (h *MetaHandler) GetHealth(c fiber.Ctx) error {
	statuses, uptime := h.monitor.Status()

	allConnected := true
	for _, s := range statuses {
		if !s.WSConnected {
			allConnected = false
			break
		}
	}

	httpStatus := fiber.StatusOK
	if !allConnected {
		httpStatus = fiber.StatusServiceUnavailable
	}

	return c.Status(httpStatus).JSON(fiber.Map{
		"status":    map[bool]string{true: "ok", false: "degraded"}[allConnected],
		"uptime_s":  int(uptime.Seconds()),
		"exchanges": statuses,
	})
}

// GET /v1/pairs
func (h *MetaHandler) GetPairs(c fiber.Ctx) error {
	pairs := h.monitor.Pairs()

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"count": len(pairs),
		"pairs": pairs,
	})
}
