ALTER TABLE user_channels
    ADD COLUMN tags TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[];

CREATE INDEX IF NOT EXISTS idx_user_channels_tags ON user_channels USING GIN (tags);
