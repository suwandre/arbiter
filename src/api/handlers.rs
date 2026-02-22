use super::models::{PairScoresResponse, ScoresResponse};
use crate::scoring::ScoringEngine;
use axum::{
    extract::{Path, State},
    http::StatusCode,
    response::Json,
};
use std::sync::Arc;

/// GET /health — simple liveness check
pub async fn health() -> &'static str {
    "OK"
}

/// GET /scores — all opportunities across all pairs and exchanges
pub async fn get_all_scores(State(engine): State<Arc<ScoringEngine>>) -> Json<ScoresResponse> {
    let scores = engine.compute_scores();
    Json(ScoresResponse { scores })
}

/// GET /scores/:pair — opportunities for a specific pair (e.g. BTCUSDT)
pub async fn get_pair_scores(
    State(engine): State<Arc<ScoringEngine>>,
    Path(pair): Path<String>,
) -> Result<Json<PairScoresResponse>, StatusCode> {
    let pair = pair.to_uppercase();
    let scores: Vec<_> = engine
        .compute_scores()
        .into_iter()
        .filter(|s| s.pair == pair)
        .collect();

    if scores.is_empty() {
        return Err(StatusCode::NOT_FOUND);
    }

    Ok(Json(PairScoresResponse { pair, scores }))
}
