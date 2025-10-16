CREATE TABLE IF NOT EXISTS business_metrics (
    id          BIGSERIAL PRIMARY KEY,
    event       TEXT NOT NULL,
    user_id     BIGINT REFERENCES users(id),
    channel_id  BIGINT REFERENCES channels(id),
    metadata    JSONB,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS business_metrics_event_idx ON business_metrics (event);
CREATE INDEX IF NOT EXISTS business_metrics_occurred_at_idx ON business_metrics (occurred_at);
