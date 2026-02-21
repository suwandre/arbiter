use super::Exchange;
use crate::errors::ExchangeError;
use crate::models::FundingRate;
use async_trait::async_trait;
use serde::Deserialize;

/// The raw JSON shape Binance sends back
#[derive(Debug, Deserialize)]
struct PremiumIndexResponse {
    symbol: String,

    #[serde(rename = "lastFundingRate")]
    last_funding_rate: String,

    #[serde(rename = "nextFundingTime")]
    next_funding_time: u64,
}

pub struct Binance {
    client: reqwest::Client,
}

impl Binance {
    pub fn new() -> Self {
        Self {
            client: reqwest::Client::new(),
        }
    }
}

#[async_trait]
impl Exchange for Binance {
    fn name(&self) -> &'static str {
        "binance"
    }

    async fn fetch_funding_rate(&self, pair: &str) -> Result<FundingRate, ExchangeError> {
        let url = format!(
            "https://fapi.binance.com/fapi/v1/premiumIndex?symbol={}",
            pair
        );

        let response = self
            .client
            .get(&url)
            .send()
            .await
            .map_err(|e| ExchangeError::Http(e))?
            .json::<PremiumIndexResponse>()
            .await
            .map_err(|e| ExchangeError::Http(e))?;

        let rate = response
            .last_funding_rate
            .parse::<f64>()
            .map_err(|e| ExchangeError::UnexpectedData(e.to_string()))?;

        let next_funding_ms = response.next_funding_time;

        Ok(FundingRate {
            exchange: self.name(),
            pair: response.symbol,
            rate,
            next_funding_ms,
        })
    }
}
