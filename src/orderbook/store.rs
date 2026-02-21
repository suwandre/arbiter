use crate::orderbook::OrderBook;
use dashmap::DashMap;
use std::sync::Arc;

#[derive(Clone)]
pub struct OrderBookStore {
    inner: Arc<DashMap<String, OrderBook>>,
}

impl OrderBookStore {
    pub fn new() -> Self {
        Self {
            inner: Arc::new(DashMap::new()),
        }
    }

    /// Key format e.g.: "binance:BTCUSDT"
    fn key(exchange: &str, pair: &str) -> String {
        format!("{}:{}", exchange, pair)
    }

    /// Writes the new order book into the store
    pub fn update(&self, ob: OrderBook) {
        self.inner
            .insert(Self::key(ob.exchange, ob.pair.as_str()), ob);
    }

    /// Gets an order book from the store
    pub fn get(&self, exchange: &str, pair: &str) -> Option<OrderBook> {
        self.inner
            .get(&Self::key(exchange, pair))
            .map(|r| r.clone())
    }

    /// Gets all stored order books (for the scoring engine later)
    pub fn all(&self) -> Vec<OrderBook> {
        self.inner.iter().map(|r| r.value().clone()).collect()
    }
}
