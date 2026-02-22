use crate::scoring::ExchangeScore;
use serde::Serialize;

/// Response for GET /scores
#[derive(Serialize)]
pub struct ScoresResponse {
    pub scores: Vec<ExchangeScore>,
}

/// Response for GET /scores/:pair
#[derive(Serialize)]
pub struct PairScoresResponse {
    pub pair: String,
    pub scores: Vec<ExchangeScore>,
}
