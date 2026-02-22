use crate::config::Config;
use crate::orderbook::{OrderBook, OrderBookStore};
use std::collections::HashMap;

/// Composite score for a given pair across all exchanges.
/// Higher = better opportunity (negative funding + tight spreads).
#[derive(Debug, Clone)]
pub struct ExchangeScore {
    pub exchange: String,
    pub pair: String,
    pub best_bid: f64,
    pub best_ask: f64,
    pub spread_pct: f64,
    pub score: f64,
}

pub struct ScoringEngine {
    store: OrderBookStore,
}

impl ScoringEngine {
    pub fn new(store: OrderBookStore) -> Self {
        Self { store }
    }

    /// Compute arbitrage opportunities and exchange rankings for all pairs.
    /// Returns top exchanges per pair, sorted by score.
    pub fn compute_scores(&self) -> Vec<ExchangeScore> {
        let mut scores = Vec::<ExchangeScore>::new();

        for orderbook in self.store.all() {
            if let (Some(best_bid), Some(best_ask)) = (orderbook.best_bid(), orderbook.best_ask()) {
                let spread_pct = (best_ask - best_bid) / best_bid * 100.0;
                let score = 100.0 / spread_pct - spread_pct; // simple heuristic

                scores.push(ExchangeScore {
                    exchange: orderbook.exchange.to_string(),
                    pair: orderbook.pair.clone(),
                    best_bid,
                    best_ask,
                    spread_pct,
                    score,
                });
            }
        }

        // Sort descending by score (best first)
        scores.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap());
        scores
    }
}
