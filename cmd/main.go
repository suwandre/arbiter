package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/suwandre/arbiter/api"
	"github.com/suwandre/arbiter/config"
	"github.com/suwandre/arbiter/internal/exchange"
	"github.com/suwandre/arbiter/internal/scorer"
)

func main() {
	// ── 1. Logger setup ─────────────────────────────────────────
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	// ── 2. Config ────────────────────────────────────────────────
	cfg := config.Load()
	log.Info().Msg("config loaded")

	// ── 3. Exchange adapters ──────────────────────────────────────
	exchanges := []exchange.Exchange{
		exchange.NewBinanceAdapter(cfg.BinanceKey),
		exchange.NewBybitAdapter(cfg.BybitKey),
	}
	log.Info().Int("count", len(exchanges)).Msg("exchange adapters initialized")

	// ── 4. Scorer ─────────────────────────────────────────────────
	sc := scorer.NewScorer(exchanges)

	// ── 5. Fiber app ──────────────────────────────────────────────
	app := fiber.New(fiber.Config{
		AppName:      "Arbiter",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	})

	// ── 6. Routes ─────────────────────────────────────────────────
	api.SetupRoutes(app, sc)

	// ── 7. Graceful shutdown ──────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-quit
		log.Info().Msg("shutdown signal received")

		if err := app.Shutdown(); err != nil {
			log.Error().Err(err).Msg("error during shutdown")
		}
	}()

	// ── 8. Start server ───────────────────────────────────────────
	log.Info().Str("port", cfg.AppPort).Msg("starting server")

	if err := app.Listen(":" + cfg.AppPort); err != nil {
		log.Fatal().Err(err).Msg("server failed to start")
	}
}
