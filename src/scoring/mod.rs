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

    /// Formats a price with enough decimal places to always show
    /// at least 4 significant digits, regardless of magnitude.
    /// e.g. 68074.30 → "68074.30", 0.00002341 → "0.00002341".
    /// This helps tickers with smaller prices to not show as "0.0".
    fn format_price(price: f64) -> String {
        if price == 0.0 {
            return "0.00".to_string();
        }

        // How many decimal places until we hit the first significant digit
        let magnitude = -price.log10().floor() as i32;

        // For prices >= $1: always show 2 dp (e.g. $68074.30)
        // For prices < $1: show enough dp to expose 4 significant digits
        let decimals = if magnitude < 0 {
            2
        } else {
            (magnitude + 4) as usize
        };

        format!("{:.prec$}", price, prec = decimals)
    }
}
