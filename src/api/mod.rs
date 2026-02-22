pub mod handlers;
pub mod models;
pub mod router;

use crate::config::Config;
use crate::scoring::ScoringEngine;
use std::net::SocketAddr;
use std::sync::Arc;

pub struct ApiServer {
    engine: Arc<ScoringEngine>,
}

impl ApiServer {
    /// Wraps the scoring engine in an Arc for shared handler access.
    pub fn new(engine: ScoringEngine) -> Self {
        Self {
            engine: Arc::new(engine),
        }
    }

    /// Binds the server to the configured port and starts serving.
    pub async fn run(self, config: Config) -> anyhow::Result<()> {
        let app = router::build(Arc::clone(&self.engine));
        let addr = SocketAddr::from(([0, 0, 0, 0], config.api_port));

        tracing::info!("API server listening on http://{}", addr);

        let listener = tokio::net::TcpListener::bind(addr).await?;
        axum::serve(listener, app).await?;

        Ok(())
    }
}
