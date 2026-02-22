use super::handlers;
use crate::scoring::ScoringEngine;
use axum::routing::get;
use axum::Router;
use std::sync::Arc;

/// Builds and returns the full Axum router with all routes and shared state.
pub fn build(engine: Arc<ScoringEngine>) -> Router {
    Router::new()
        .route("/health", get(handlers::health))
        .route("/scores", get(handlers::get_all_scores))
        .route("/scores/:pair", get(handlers::get_pair_scores))
        .with_state(engine)
}
