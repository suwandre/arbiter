package api

import (
	"github.com/gofiber/fiber/v3"
	"github.com/suwandre/arbiter/api/handlers"
	"github.com/suwandre/arbiter/internal/scheduler"
)

func SetupRoutes(app *fiber.App, scheduler *scheduler.Scheduler) {
	scoreHandler := handlers.NewScoreHandler(scheduler)

	v1 := app.Group("/v1")

	v1.Get("/scores/:pair", scoreHandler.GetScores)
}
