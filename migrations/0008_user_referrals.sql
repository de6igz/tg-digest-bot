ALTER TABLE users
    ADD COLUMN referral_code TEXT,
    ADD COLUMN referrals_count INT NOT NULL DEFAULT 0,
    ADD COLUMN referred_by BIGINT REFERENCES users(id);

UPDATE users
SET referral_code = SUBSTR(UPPER(md5(random()::text || id::text)), 1, 8)
WHERE referral_code IS NULL;

ALTER TABLE users
    ALTER COLUMN referral_code SET NOT NULL;

ALTER TABLE users
    ADD CONSTRAINT users_referral_code_key UNIQUE (referral_code);

CREATE INDEX idx_users_referred_by ON users(referred_by);
