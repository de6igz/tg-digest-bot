ALTER TABLE users
    ADD COLUMN role TEXT NOT NULL DEFAULT 'free',
    ADD COLUMN manual_requests_total INT NOT NULL DEFAULT 0,
    ADD COLUMN manual_requests_today INT NOT NULL DEFAULT 0,
    ADD COLUMN manual_requests_date DATE;
