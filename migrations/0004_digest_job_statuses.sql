CREATE TABLE IF NOT EXISTS digest_job_statuses (
    job_id TEXT PRIMARY KEY,
    attempts INT NOT NULL DEFAULT 0,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS digest_job_statuses_delivered_idx
    ON digest_job_statuses (delivered_at);
