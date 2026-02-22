mod api;
mod config;
mod errors;
mod exchanges;
mod models;
mod orderbook;
mod scoring;

use config::Config;
use exchanges::binance::Binance;
use exchanges::bybit::Bybit;
use exchanges::Exchange;
use orderbook::store::OrderBookStore;
use scoring::ScoringEngine;

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

    // ── 3. Spawn scoring engine loop ───────────────────────────────
    let scoring_engine = ScoringEngine::new(store.clone());

    tokio::spawn(async move {
        // wait for streams to fill store before showing first score instead of showing empty list
        tokio::time::sleep(tokio::time::Duration::from_secs(3)).await;

        let mut interval = tokio::time::interval(tokio::time::Duration::from_secs(5));
        loop {
            interval.tick().await;
            let scores = scoring_engine.compute_scores();
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

    // ── 4. Keep main alive until Ctrl+C ───────────────────────────
    tokio::signal::ctrl_c().await.unwrap();
    tracing::info!("Shutting down...");
}
