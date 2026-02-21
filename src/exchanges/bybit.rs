use super::Exchange;
use crate::errors::ExchangeError;
use crate::models::FundingRate;
use async_trait::async_trait;
use serde::Deserialize;

#[derive(Debug, Deserialize)]
struct BybitResponse {
    #[serde(rename = "retCode")]
    ret_code: i32,
    result: BybitResult,
}

#[derive(Debug, Deserialize)]
struct BybitResult {
    list: Vec<BybitTicker>,
}

#[derive(Debug, Deserialize)]
struct BybitTicker {
    symbol: String,

    #[serde(rename = "fundingRate")]
    funding_rate: String,

    #[serde(rename = "nextFundingTime")]
    next_funding_time: String,
}

pub struct Bybit {
    client: reqwest::Client,
}

impl Bybit {
    pub fn new() -> Self {
        Self {
            client: reqwest::Client::new(),
        }
    }
}

#[async_trait]
impl Exchange for Bybit {
    fn name(&self) -> &'static str {
        "bybit"
    }

    async fn fetch_funding_rate(&self, pair: &str) -> Result<FundingRate, ExchangeError> {
        let url = format!(
            "https://api.bybit.com/v5/market/tickers?category=linear&symbol={}",
            pair
        );

        let response = self
            .client
            .get(&url)
            .send()
            .await
            .map_err(|e| ExchangeError::Http(e))?
            .json::<BybitResponse>()
            .await
            .map_err(|e| ExchangeError::Http(e))?;

        // Bybit signals errors via retCode, not just HTTP status
        if response.ret_code != 0 {
            return Err(ExchangeError::UnexpectedData(format!(
                "Bybit retCode: {}",
                response.ret_code
            )));
        }

        // list always has one item when querying by symbol
        let ticker = response.result.list.into_iter().next().ok_or_else(|| {
            ExchangeError::UnexpectedData(format!("Bybit returned empty list for {}", pair))
        })?;

        let rate = ticker
            .funding_rate
            .parse::<f64>()
            .map_err(|e| ExchangeError::UnexpectedData(e.to_string()))?;

        let next_funding_ms = ticker
            .next_funding_time
            .parse::<u64>()
            .map_err(|e| ExchangeError::UnexpectedData(e.to_string()))?;

        Ok(FundingRate {
            exchange: self.name(),
            pair: ticker.symbol,
            rate,
            next_funding_ms,
        })
    }
}
