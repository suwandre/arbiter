mod config;
mod errors;
mod exchanges;
mod models;
mod orderbook;

use config::Config;
use exchanges::Exchange;
use exchanges::binance::Binance;
use exchanges::bybit::Bybit;
use orderbook::store::OrderBookStore;

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();

    let exchanges: Vec<Box<dyn Exchange>> = vec![Box::new(Binance::new()), Box::new(Bybit::new())];

    for ex in &exchanges {
        match ex.fetch_funding_rate("BTCUSDT").await {
            Ok(fr) => tracing::info!(
                "[{}] BTCUSDT funding rate: {:.4}%",
                fr.exchange,
                fr.rate * 100.0
            ),
            Err(e) => tracing::error!("[{}] Failed: {}", ex.name(), e)
        }
    }

    let config = Config::from_env();
    let store = OrderBookStore::new();

    tracing::info!(
        "Arbiter starting — watching pairs: {:?} on port {}",
        config.pairs,
        config.api_port
    );
}
