package api

import (
	"github.com/gofiber/fiber/v3"
	"github.com/suwandre/arbiter/api/handlers"
	"github.com/suwandre/arbiter/internal/scorer"
)

func SetupRoutes(app *fiber.App, scorer *scorer.Scorer) {
	scoreHandler := handlers.NewScoreHandler(scorer)

	v1 := app.Group("/v1")

	v1.Get("/scores/:pair", scoreHandler.GetScores)
}
