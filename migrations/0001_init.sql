CREATE TABLE users (
  id BIGSERIAL PRIMARY KEY,
  tg_user_id BIGINT UNIQUE NOT NULL,
  locale TEXT DEFAULT 'ru-RU',
  tz TEXT DEFAULT 'Europe/Amsterdam',
  daily_time TIME DEFAULT '09:00',
  created_at TIMESTAMPTZ DEFAULT now(),
  updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE channels (
  id BIGSERIAL PRIMARY KEY,
  tg_channel_id BIGINT,
  alias TEXT UNIQUE NOT NULL,
  title TEXT,
  is_allowed BOOLEAN DEFAULT TRUE,
  created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE user_channels (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
  channel_id BIGINT REFERENCES channels(id) ON DELETE CASCADE,
  muted BOOLEAN DEFAULT FALSE,
  added_at TIMESTAMPTZ DEFAULT now(),
  UNIQUE(user_id, channel_id)
);

CREATE TABLE posts (
  id BIGSERIAL PRIMARY KEY,
  channel_id BIGINT REFERENCES channels(id) ON DELETE CASCADE,
  tg_msg_id BIGINT NOT NULL,
  published_at TIMESTAMPTZ NOT NULL,
  url TEXT NOT NULL,
  text_trunc TEXT,
  raw_meta_json JSONB,
  hash TEXT,
  created_at TIMESTAMPTZ DEFAULT now(),
  UNIQUE(channel_id, tg_msg_id)
);

CREATE INDEX idx_posts_channel_time ON posts(channel_id, published_at DESC);

CREATE TABLE post_summaries (
  id BIGSERIAL PRIMARY KEY,
  post_id BIGINT REFERENCES posts(id) ON DELETE CASCADE,
  lang TEXT DEFAULT 'ru',
  headline TEXT,
  bullets_json JSONB,
  score NUMERIC,
  created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE user_digests (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
  date DATE NOT NULL,
  delivered_at TIMESTAMPTZ,
  items_count INT DEFAULT 0,
  UNIQUE(user_id, date)
);

CREATE TABLE user_digest_items (
  id BIGSERIAL PRIMARY KEY,
  digest_id BIGINT REFERENCES user_digests(id) ON DELETE CASCADE,
  post_id BIGINT REFERENCES posts(id) ON DELETE CASCADE,
  summary_id BIGINT REFERENCES post_summaries(id) ON DELETE SET NULL,
  rank INT,
  clicked BOOLEAN DEFAULT FALSE
);
