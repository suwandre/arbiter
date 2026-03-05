package api

import (
	"github.com/gofiber/fiber/v3"
	"github.com/suwandre/arbiter/api/handlers"
	"github.com/suwandre/arbiter/internal/scorer"
	"github.com/suwandre/arbiter/internal/stream"
)

func SetupRoutes(app *fiber.App, data stream.MonitorSource, sc *scorer.Scorer) {
	scoreHandler := handlers.NewScoreHandler(data, sc)
	fundingHandler := handlers.NewFundingHandler(data)
	anomalyHandler := handlers.NewAnomalyHandler(data)
	metaHandler := handlers.NewMetaHandler(data)

	v1 := app.Group("/v1")

	v1.Get("/health", metaHandler.GetHealth)
	v1.Get("/pairs", metaHandler.GetPairs)
	v1.Get("/scores/:pair", scoreHandler.GetScores)
	v1.Get("/funding/:pair", fundingHandler.GetFundingCost)
	v1.Get("/funding/:pair/arb", fundingHandler.GetFundingArb)
	v1.Get("/funding/:pair/diff", fundingHandler.GetFundingDiff)
	v1.Get("/funding/:pair/basis", fundingHandler.GetBasis)
	v1.Get("/funding/:pair/cross-basis", fundingHandler.GetCrossBasis)
	v1.Get("/anomaly/:pair", anomalyHandler.GetAnomaly)
}
