-- Stage 7: extend api_metrics with access-control dimensions. Columns are
-- added with defaults and are nullable where appropriate, so existing rows are
-- preserved unchanged.
ALTER TABLE api_metrics ADD COLUMN IF NOT EXISTS api_key_missing BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE api_metrics ADD COLUMN IF NOT EXISTS account_id     BIGINT;
ALTER TABLE api_metrics ADD COLUMN IF NOT EXISTS rate_limited   BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE api_metrics ADD COLUMN IF NOT EXISTS quota_exceeded BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_api_metrics_account_id ON api_metrics (account_id);
