mod exchanges;
mod errors;
mod models;
mod orderbook;
mod config;

use config::Config;
use orderbook::store::OrderBookStore;

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
}
