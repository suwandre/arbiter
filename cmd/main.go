package main

import (
	"context"
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
	"github.com/suwandre/arbiter/internal/scheduler"
	"github.com/suwandre/arbiter/internal/scorer"
)

func main() {
	// ── 1. Logger setup
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	// ── 2. Root context setup
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── 3. Config
	cfg := config.Load()
	log.Info().Msg("config loaded")

	// ── 4. Exchange adapters
	exchanges := []exchange.Exchange{
		exchange.NewBinanceAdapter(cfg.BinanceKey),
		exchange.NewBybitAdapter(cfg.BybitKey),
	}
	log.Info().Int("count", len(exchanges)).Msg("exchange adapters initialized")

	// ── 5. Scorer + Scheduler
	sc := scorer.NewScorer(exchanges)
	sched := scheduler.NewScheduler(sc, []string{
		"BTCUSDT",
		"ETHUSDT",
		"SOLUSDT",
	}, 10*time.Second)

	sched.Start(ctx)
	defer sched.Stop()

	// ── 6. Fiber app
	app := fiber.New(fiber.Config{
		AppName:      "Arbiter",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	})

	// ── 7. Routes
	api.SetupRoutes(app, sched)

	// ── 8. Graceful shutdown listener
	go func() {
		<-ctx.Done()
		log.Info().Msg("shutdown signal received")
		if err := app.Shutdown(); err != nil {
			log.Error().Err(err).Msg("error during shutdown")
		}
	}()

	// ── 9. Start server (blocking)
	log.Info().Str("port", cfg.AppPort).Msg("starting server")
	if err := app.Listen(":" + cfg.AppPort); err != nil {
		log.Fatal().Err(err).Msg("server failed to start")
	}
}
