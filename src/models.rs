#[derive(Debug, Clone)]
pub struct FundingRate {
    pub exchange: &'static str,
    pub pair: String,
    pub rate: f64,
    pub next_funding_ms: u64,
}