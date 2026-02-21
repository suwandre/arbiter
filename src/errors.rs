use async_trait::async_trait;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum ExchangeError {
    #[error("WebSocket error: {0}")]
    WebSocket(String),

    #[error("HTTP error: {0}")]
    Http(#[from] reqwest::Error),

    #[error("JSON parse error: {0}")]
    Parse(#[from] serde_json::Error),

    #[error("Unexpected data from exchange: {0}")]
    UnexpectedData(String),
}