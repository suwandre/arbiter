mod config;
mod errors;
mod exchanges;
mod models;
mod orderbook;

use config::Config;
use exchanges::Exchange;
use exchanges::binance::Binance;
use orderbook::store::OrderBookStore;

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();

    let binance = Binance::new();
    let result = binance.fetch_funding_rate("BTCUSDT").await;
    match result {
        Ok(fr) => tracing::info!("✅ Success: {:?}", fr),
        Err(e) => tracing::error!("❌ Failed: {}", e),
    }

    let config = Config::from_env();
    let store = OrderBookStore::new();

    tracing::info!(
        "Arbiter starting — watching pairs: {:?} on port {}",
        config.pairs,
        config.api_port
    );
}
