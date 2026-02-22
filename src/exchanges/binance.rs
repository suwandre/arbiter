use std::collections::BTreeMap;

use super::Exchange;
use crate::errors::ExchangeError;
use crate::models::FundingRate;
use crate::orderbook::OrderBook;
use crate::{config::Config, orderbook::OrderBookStore};
use async_trait::async_trait;
use futures_util::{SinkExt, StreamExt};
use ordered_float::OrderedFloat;
use serde::Deserialize;
use tokio_tungstenite::{connect_async, tungstenite::Message};

/// The raw JSON shape Binance sends back
#[derive(Debug, Deserialize)]
struct PremiumIndexResponse {
    symbol: String,

    #[serde(rename = "lastFundingRate")]
    last_funding_rate: String,

    #[serde(rename = "nextFundingTime")]
    next_funding_time: u64,
}

/// Raw order book delta message sent directly by Binance's
/// single-stream WebSocket endpoint. No outer wrapper present.
#[derive(Debug, Deserialize)]
struct DepthData {
    /// Event timestamp in milliseconds
    #[serde(rename = "E")]
    event_time: u64,

    /// Trading pair symbol e.g. "BTCUSDT"
    #[serde(rename = "s")]
    symbol: String,

    /// Bid levels: [[price, quantity], ...]
    #[serde(rename = "b")]
    bids: Vec<[String; 2]>,

    /// Ask levels: [[price, quantity], ...]
    #[serde(rename = "a")]
    asks: Vec<[String; 2]>,
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

/// Connects to Binance's depth WebSocket stream for a single pair,
/// reads messages in a loop, and logs the raw JSON.
/// Returns an error if the connection fails or the stream closes unexpectedly.
/// Runs indefinitely until the stream closes or an error occurs.
async fn stream_pair(
    name: &'static str,
    pair: String,
    store: OrderBookStore,
) -> Result<(), ExchangeError> {
    let url = format!("wss://fstream.binance.com/ws/{pair}@depth20@100ms");

    tracing::info!("[{name}] {pair} stream connecting to {url}");

    let (ws_stream, _) = connect_async(&url)
        .await
        .map_err(|e| ExchangeError::WebSocket(e.to_string()))?;

    let (_, mut read_stream) = ws_stream.split();

    while let Some(msg) = read_stream.next().await {
        let msg = msg.map_err(|e| ExchangeError::WebSocket(e.to_string()))?;

        if let Message::Text(text) = msg {
            let depth: DepthData = match serde_json::from_str(&text) {
                Ok(d) => d,
                Err(e) => {
                    tracing::warn!("[{name}] parse error: {e} — raw: {text}");
                    continue; // skip this message, keep stream alive
                }
            };

            let mut orderbook = OrderBook {
                exchange: name,
                pair: depth.symbol.clone(),
                bids: BTreeMap::new(),
                asks: BTreeMap::new(),
                updated_ms: depth.event_time,
            };

            // Parse bids (BTree keeps ascending, so highest bid will be in the last position)
            for [price_str, qty_str] in depth.bids {
                let price = price_str.parse::<f64>().unwrap_or(0.0);
                let qty = qty_str.parse::<f64>().unwrap_or(0.0);
                orderbook.bids.insert(OrderedFloat(price), qty);
            }

            // Parse asks (BTree keeps ascending, so lowest ask is already in the first position)
            for [price_str, qty_str] in depth.asks {
                let price = price_str.parse::<f64>().unwrap_or(0.0);
                let qty = qty_str.parse::<f64>().unwrap_or(0.0);
                orderbook.asks.insert(OrderedFloat(price), qty);
            }

            store.update(orderbook.clone());
            tracing::debug!(
                "[{name}] {} bids: {} asks: {}",
                orderbook.pair,
                orderbook.bids.len(),
                orderbook.asks.len()
            );
        }
    }

    tracing::warn!("[{name}] {pair} stream closed");
    Ok(())
}

#[async_trait]
impl Exchange for Binance {
    fn name(&self) -> &'static str {
        "binance"
    }

    /// Fetches the current funding rate for a given pair via REST.
    /// Hits Binance's premiumIndex endpoint and maps the response
    /// into the normalized FundingRate model.
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

    /// Spawns one tokio task per configured pair, each maintaining
    /// a persistent WebSocket connection to Binance's order book stream.
    /// Errors inside each task are logged but do not crash the others.
    async fn run_orderbook_stream(
        &self,
        config: &Config,
        store: OrderBookStore,
    ) -> Result<(), ExchangeError> {
        for pair in &config.pairs {
            let store = store.clone();
            let pair = pair.to_lowercase();
            let name = self.name();

            tokio::spawn(async move {
                if let Err(e) = stream_pair(name, pair, store).await {
                    tracing::error!("[{name}] stream error: {e}");
                }
            });
        }

        Ok(())
    }
}
