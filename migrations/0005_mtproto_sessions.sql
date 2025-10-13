CREATE TABLE mtproto_sessions (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  data BYTEA,
  updated_at TIMESTAMPTZ DEFAULT now()
);
