use crate::config::Config;
use crate::errors::ExchangeError;
use crate::models::FundingRate;
use crate::orderbook::OrderBookStore;
use async_trait::async_trait;

pub mod binance;
pub mod bybit;

#[async_trait]
pub trait Exchange: Send + Sync {
    fn name(&self) -> &'static str;

    async fn fetch_funding_rate(&self, pair: &str) -> Result<FundingRate, ExchangeError>;

    /// Spawn a tokio task that connects to this exchange's order book Websocket
    /// and continuously updates the store.
    async fn run_orderbook_stream(
        &self,
        config: &Config,
        store: OrderBookStore,
    ) -> Result<(), ExchangeError>;
}
