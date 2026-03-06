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
	"github.com/suwandre/arbiter/internal/scorer"
	"github.com/suwandre/arbiter/internal/stream"
)

func main() {
	// ── 1. Logger setup
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	// ── 2. Root context setup
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── 3. Config
	cfg := config.Load()
	log.Info().Msg("config loaded")

	// ── 4. Exchange adapters
	mexc, err := exchange.NewMexcAdapter(cfg.MexcKey)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize MEXC adapter")
	}

	exchanges := []exchange.StreamingExchange{
		exchange.NewBinanceAdapter(cfg.BinanceKey),
		exchange.NewBybitAdapter(cfg.BybitKey),
		mexc,
	}
	log.Info().Int("count", len(exchanges)).Msg("exchange adapters initialized")

	// Convert StreamingExchange slice to Exchange slice for scorer
	baseExchanges := make([]exchange.Exchange, len(exchanges))
	for i, ex := range exchanges {
		baseExchanges[i] = ex
	}

	// ── 5. Scorer + Stream Manager
	sc := scorer.NewScorer(baseExchanges)
	manager := stream.NewManager(exchanges, []string{
		"BTCUSDT",
		"ETHUSDT",
		"SOLUSDT",
		"BNBUSDT",
		"TRXUSDT",
		"XRPUSDT",
		"DOGEUSDT",
		"ADAUSDT",
	})

	manager.Start(ctx)
	defer manager.Stop()

	// ── 6. Fiber app
	app := fiber.New(fiber.Config{
		AppName:      "Arbiter",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	})

	// ── 7. Routes
	api.SetupRoutes(app, manager, sc)

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
