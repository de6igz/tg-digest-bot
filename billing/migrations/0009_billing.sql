BEGIN;

CREATE TABLE billing_accounts (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL UNIQUE,
    balance     BIGINT NOT NULL DEFAULT 0,
    currency    TEXT   NOT NULL DEFAULT 'RUB',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE billing_invoices (
    id               BIGSERIAL PRIMARY KEY,
    account_id       BIGINT NOT NULL REFERENCES billing_accounts(id) ON DELETE CASCADE,
    amount           BIGINT NOT NULL CHECK (amount > 0),
    currency         TEXT   NOT NULL,
    description      TEXT,
    metadata         JSONB,
    status           TEXT   NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'cancelled')),
    idempotency_key  TEXT   NOT NULL UNIQUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at          TIMESTAMPTZ
);

CREATE INDEX billing_invoices_account_id_idx ON billing_invoices(account_id);
CREATE INDEX billing_invoices_status_idx ON billing_invoices(status);

CREATE TABLE billing_payments (
    id               BIGSERIAL PRIMARY KEY,
    account_id       BIGINT NOT NULL REFERENCES billing_accounts(id) ON DELETE CASCADE,
    invoice_id       BIGINT REFERENCES billing_invoices(id) ON DELETE SET NULL,
    amount           BIGINT NOT NULL CHECK (amount > 0),
    currency         TEXT   NOT NULL,
    metadata         JSONB,
    status           TEXT   NOT NULL DEFAULT 'completed' CHECK (status IN ('completed', 'failed')),
    idempotency_key  TEXT   NOT NULL UNIQUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at     TIMESTAMPTZ
);



ALTER TABLE billing_invoices
add column qr_id TEXT;

CREATE INDEX billing_payments_account_id_idx ON billing_payments(account_id);
CREATE INDEX billing_payments_invoice_id_idx ON billing_payments(invoice_id);

COMMIT;
