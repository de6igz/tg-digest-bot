CREATE TABLE mtproto_accounts (
  id BIGSERIAL PRIMARY KEY,
  pool TEXT NOT NULL DEFAULT 'default',
  name TEXT NOT NULL,
  api_id INTEGER NOT NULL,
  api_hash TEXT NOT NULL,
  phone TEXT,
  username TEXT,
  raw_json JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (pool, name)
);
