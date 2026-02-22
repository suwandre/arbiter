mod api;
mod config;
mod errors;
mod exchanges;
mod models;
mod orderbook;
mod scoring;

use std::sync::Arc;

use config::Config;
use exchanges::binance::Binance;
use exchanges::bybit::Bybit;
use exchanges::Exchange;
use orderbook::store::OrderBookStore;
use scoring::ScoringEngine;

use crate::api::ApiServer;

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();

    let config = Config::from_env();
    let store = OrderBookStore::new();

    tracing::info!(
        "Arbiter starting — watching pairs: {:?} on port {}",
        config.pairs,
        config.api_port
    );

    // ── 0. Create shutdown broadcast channel (sender lives in main) ─────────
    let (shutdown_tx, _) = tokio::sync::broadcast::channel::<()>(1);

    // Clone sender for the signal task
    let shutdown_tx_signal = shutdown_tx.clone();

    // Spawn signal listener (Ctrl+C → broadcast shutdown)
    tokio::spawn(async move {
        tokio::signal::ctrl_c()
            .await
            .expect("failed to install Ctrl+C handler");
        let _ = shutdown_tx_signal.send(()); // Broadcast to ALL subscribers
    });

    // ── 1. Fetch funding rates (one-off at startup) ────────────────
    let exchanges: Vec<Box<dyn Exchange>> = vec![Box::new(Binance::new()), Box::new(Bybit::new())];

    for ex in &exchanges {
        match ex.fetch_funding_rate("BTCUSDT").await {
            Ok(fr) => tracing::info!(
                "[{}] BTCUSDT funding rate: {:.4}%",
                fr.exchange,
                fr.rate * 100.0
            ),
            Err(e) => tracing::error!("[{}] Failed: {}", ex.name(), e),
        }
    }

    // ── 2. Spawn WebSocket order book streams ──────────────────────
    for ex in &exchanges {
        if let Err(e) = ex.run_orderbook_stream(&config, store.clone()).await {
            tracing::error!("[{}] Failed to start stream: {}", ex.name(), e);
        }
    }

    // ── 3. Create scoring engine ───────────────────────────────────
    let scoring_engine = Arc::new(ScoringEngine::new(store.clone()));

    // ── 4. Spawn scoring loop ──────────────────────────────────────
    let scoring_engine_clone = Arc::clone(&scoring_engine);
    tokio::spawn(async move {
        // wait for streams to fill store before showing first score
        tokio::time::sleep(tokio::time::Duration::from_secs(3)).await;
        let mut interval = tokio::time::interval(tokio::time::Duration::from_secs(5));
        loop {
            interval.tick().await;
            let scores = scoring_engine_clone.compute_scores();
            tracing::info!("=== TOP OPPORTUNITIES ===");
            for score in scores.iter().take(5) {
                tracing::info!(
                    "[{}] {}: bid=${} ask=${} spread={:.6}% score={:.1}",
                    score.exchange,
                    score.pair,
                    ScoringEngine::format_price(score.best_bid),
                    ScoringEngine::format_price(score.best_ask),
                    score.spread_pct,
                    score.score
                );
            }
        }
    });

    // ── 5. Spawn API server with its shutdown receiver ─────────────
    let config_clone = config.clone();
    let shutdown_rx_api = shutdown_tx.subscribe(); // subscribe from original sender
    let scoring_engine_clone = Arc::clone(&scoring_engine);

    tokio::spawn(async move {
        let api_server = ApiServer::new(scoring_engine_clone);
        if let Err(e) = api_server
            .run_with_broadcast(config_clone, shutdown_rx_api)
            .await
        {
            tracing::error!("API server error: {}", e);
        }
    });

    // ── 6. Block main until shutdown signal ────────────────────────
    tokio::time::sleep(tokio::time::Duration::from_secs(2)).await;
    tracing::info!("✅ All services running. Press Ctrl+C for graceful shutdown.");

    // Main also subscribes to the same broadcast channel
    let mut shutdown_rx_main = shutdown_tx.subscribe();
    let _ = shutdown_rx_main.recv().await;
    tracing::info!("🛑 Shutdown signal received — waiting for graceful close...");

    tokio::time::sleep(tokio::time::Duration::from_secs(10)).await;
    tracing::info!("✨ Arbiter shutdown complete.");
}
