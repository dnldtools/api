-- Stage 7: API-key authentication + access control.
-- Adds accounts, api_keys, plan_policies (configuration), and quota_usage
-- (daily/monthly request usage counters). This migration only ADDs tables; it
-- never touches api_metrics, so existing metrics data is preserved.

-- Accounts are tenants. There is no billing in this stage.
CREATE TABLE IF NOT EXISTS accounts (
    id         BIGSERIAL   PRIMARY KEY,
    name       TEXT        NOT NULL,
    role       TEXT        NOT NULL DEFAULT 'user',      -- user | admin
    plan       TEXT        NOT NULL DEFAULT 'trial',     -- trial | free | pro
    status     TEXT        NOT NULL DEFAULT 'active',    -- active | suspended | disabled
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- API keys. Only the SHA-256 hash is stored; the raw key is never persisted.
CREATE TABLE IF NOT EXISTS api_keys (
    id           BIGSERIAL   PRIMARY KEY,
    account_id   BIGINT      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    key_hash     TEXT        NOT NULL UNIQUE,
    status       TEXT        NOT NULL DEFAULT 'active',  -- active | revoked | expired
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_api_keys_account_id ON api_keys (account_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash   ON api_keys (key_hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_status     ON api_keys (status);

-- Plan policies are runtime configuration (rate limit + quota). Values mirror
-- internal/plans.Defaults(); editing a row here overrides the built-in default
-- at startup. rate_window uses Go duration syntax (e.g. '1m', '30s').
CREATE TABLE IF NOT EXISTS plan_policies (
    plan          TEXT    PRIMARY KEY,
    rate_limit    INTEGER NOT NULL,
    rate_window   TEXT    NOT NULL,
    daily_quota   BIGINT  NOT NULL,
    monthly_quota BIGINT  NOT NULL
);

INSERT INTO plan_policies (plan, rate_limit, rate_window, daily_quota, monthly_quota) VALUES
    ('trial', 10,   '1m', 100,        1000),
    ('free',  60,   '1m', 10000,      100000),
    ('pro',   600,  '1m', 1000000,    10000000)
ON CONFLICT (plan) DO NOTHING;

-- quota_usage holds aggregated per-period request counters. PostgreSQL is the
-- source of truth; one row per (account, plan, period_type, period_start).
CREATE TABLE IF NOT EXISTS quota_usage (
    id             BIGSERIAL   PRIMARY KEY,
    account_id     BIGINT      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    plan           TEXT        NOT NULL,
    period_type    TEXT        NOT NULL,               -- daily | monthly
    period_start   TIMESTAMPTZ NOT NULL,               -- UTC start of the period
    total_requests BIGINT      NOT NULL DEFAULT 0,
    success_count  BIGINT      NOT NULL DEFAULT 0,
    failed_count   BIGINT      NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (account_id, plan, period_type, period_start)
);

CREATE INDEX IF NOT EXISTS idx_quota_usage_account_period
    ON quota_usage (account_id, period_type, period_start);
