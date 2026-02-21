pub mod store;

use ordered_float::OrderedFloat;
use std::collections::BTreeMap;
pub use store::OrderBookStore;

#[derive(Debug, Clone)]
pub struct OrderBook {
    pub exchange: &'static str,
    pub pair: String,
    // price → quantity. BTreeMap keeps keys sorted ascending.
    pub bids: BTreeMap<OrderedFloat<f64>, f64>,
    pub asks: BTreeMap<OrderedFloat<f64>, f64>,
    pub updated_ms: u64,
}

impl OrderBook {
    /// Highest bid price (best price a buyer will pay)
    pub fn best_bid(&self) -> Option<f64> {
        // next_back() returns the highest key in the map
        self.bids.keys().next_back().map(|p| p.into_inner())
    }

    /// Lowest ask price (best price a seller will accept)
    pub fn best_ask(&self) -> Option<f64> {
        // next() returns the lowest key in the map
        self.asks.keys().next().map(|p| p.into_inner())
    }

    /// Spread between best ask and best bid
    pub fn spread(&self) -> Option<f64> {
        Some(self.best_ask()? - self.best_bid()?)
    }
}
