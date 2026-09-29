CREATE TABLE IF NOT EXISTS media (
    id           BIGSERIAL   PRIMARY KEY,
    platform     TEXT        NOT NULL,
    source_url   TEXT        NOT NULL,
    object_key   TEXT        NOT NULL UNIQUE,
    bucket       TEXT        NOT NULL,
    account      TEXT        NOT NULL,
    content_type TEXT        NOT NULL DEFAULT 'application/octet-stream',
    size         BIGINT      NOT NULL DEFAULT 0,
    status       TEXT        NOT NULL DEFAULT 'ready',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_media_object_key ON media (object_key);
CREATE INDEX IF NOT EXISTS idx_media_created_at ON media (created_at);
