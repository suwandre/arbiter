use async_trait::async_trait;
use crate::errors::ExchangeError;
use crate::models::FundingRate;

pub mod binance;
pub mod bybit;

#[async_trait]
pub trait Exchange: Send + Sync {
    fn name(&self) -> &'static str;

    async fn fetch_funding_rate(
        &self,
        pair: &str,
    ) -> Result<FundingRate, ExchangeError>;
}
