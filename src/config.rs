use std::env;

#[derive(Debug, Clone)]
pub struct Config {
    pub pairs: Vec<String>,
    pub api_port: u16,
}

impl Config {
    pub fn from_env() -> Self {
        dotenvy::dotenv().ok();

        // default to BTCUSDT and ETHUSDT if PAIRS is not set
        let pairs = env::var("PAIRS")
            .unwrap_or_else(|_| "BTCUSDT,ETHUSDT".to_string())
            .split(',')
            .map(|s| s.trim().to_uppercase())
            .collect();

        let api_port = env::var("API_PORT")
            .unwrap_or_else(|_| "3000".to_string())
            .parse::<u16>()
            .expect("API_PORT must be a valid port number (1-65535)");

        Self { pairs, api_port }
    }
}
