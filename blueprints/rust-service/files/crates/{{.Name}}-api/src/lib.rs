//! HTTP routes of the service.

use axum::{Json, Router, http::StatusCode, routing::get};
use serde::Serialize;

/// Builds the application router. Add routes here.
pub fn app() -> Router {
    Router::new()
        .route("/healthz", get(healthz))
        .fallback(not_found)
}

#[derive(Debug, Serialize)]
struct Health {
    status: &'static str,
}

async fn healthz() -> Json<Health> {
    Json(Health { status: "ok" })
}

async fn not_found() -> StatusCode {
    StatusCode::NOT_FOUND
}
