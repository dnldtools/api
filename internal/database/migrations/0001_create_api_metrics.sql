-- api_metrics is the persistent source of truth for API request metrics.
-- One row is written per request. Raw API keys are never stored; client_id is
-- a SHA-256 hash used only for safe identification.
CREATE TABLE IF NOT EXISTS api_metrics (
    id              BIGSERIAL   PRIMARY KEY,
    request_id      TEXT        NOT NULL,
    status_code     INTEGER     NOT NULL,
    success         BOOLEAN     NOT NULL,
    failed          BOOLEAN     NOT NULL,
    valid_api_key   BOOLEAN     NOT NULL DEFAULT FALSE,
    invalid_api_key BOOLEAN     NOT NULL DEFAULT FALSE,
    client_id       TEXT,                  -- SHA-256 hash of the API key (never the raw key)
    endpoint        TEXT        NOT NULL,  -- request path, e.g. /v1/downloads
    platform        TEXT,                  -- platform slug when applicable (facebook)
    duration_ms     BIGINT      NOT NULL,  -- response duration in milliseconds
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_api_metrics_created_at ON api_metrics (created_at);
CREATE INDEX IF NOT EXISTS idx_api_metrics_endpoint   ON api_metrics (endpoint);
CREATE INDEX IF NOT EXISTS idx_api_metrics_status     ON api_metrics (status_code);
CREATE INDEX IF NOT EXISTS idx_api_metrics_platform   ON api_metrics (platform);
