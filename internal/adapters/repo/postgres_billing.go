package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/metrics"
)

const (
	billingCurrencyDefault = "RUB"
)

func ensureBillingAccountTx(ctx context.Context, tx pgx.Tx, userID int64) (domain.BillingAccount, error) {
	var acc domain.BillingAccount
	start := time.Now()
	err := tx.QueryRow(ctx, `
INSERT INTO billing_accounts (user_id)
VALUES ($1)
ON CONFLICT (user_id) DO UPDATE SET updated_at = now()
RETURNING id, user_id, balance, currency, created_at, updated_at
`, userID).Scan(&acc.ID, &acc.UserID, &acc.Balance.Amount, &acc.Balance.Currency, &acc.CreatedAt, &acc.UpdatedAt)
	metrics.ObserveNetworkRequest("postgres", "billing_accounts_ensure_tx", "billing_accounts", start, err)
	if err != nil {
		return domain.BillingAccount{}, err
	}
	if acc.Balance.Currency == "" {
		acc.Balance.Currency = billingCurrencyDefault
	}
	return acc, nil
}

func (p *Postgres) EnsureAccount(ctx context.Context, userID int64) (domain.BillingAccount, error) {
	ctx, cancel := p.connCtxWithParent(ctx)
	defer cancel()

	start := time.Now()
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	metrics.ObserveNetworkRequest("postgres", "begin_tx", "billing_accounts", start, err)
	if err != nil {
		return domain.BillingAccount{}, err
	}
	acc, err := ensureBillingAccountTx(ctx, tx, userID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return domain.BillingAccount{}, err
	}
	start = time.Now()
	err = tx.Commit(ctx)
	metrics.ObserveNetworkRequest("postgres", "commit", "billing_accounts", start, err)
	return acc, err
}

func (p *Postgres) GetAccountByUserID(ctx context.Context, userID int64) (domain.BillingAccount, error) {
	ctx, cancel := p.connCtxWithParent(ctx)
	defer cancel()

	acc, err := p.getAccountByUserID(ctx, userID)
	if err != nil {
		return domain.BillingAccount{}, err
	}
	return acc, nil
}

func (p *Postgres) CreateInvoice(ctx context.Context, params domain.CreateInvoiceParams) (domain.Invoice, error) {
	if params.IdempotencyKey == "" {
		return domain.Invoice{}, fmt.Errorf("idempotency key is required")
	}
	ctx, cancel := p.connCtxWithParent(ctx)
	defer cancel()

	account, err := p.getAccount(ctx, params.AccountID)
	if err != nil {
		return domain.Invoice{}, err
	}
	if params.Amount.Currency == "" {
		params.Amount.Currency = account.Balance.Currency
	}
	if account.Balance.Currency != params.Amount.Currency {
		return domain.Invoice{}, fmt.Errorf("account currency mismatch")
	}

	var meta pgtype.JSONB
	if params.Metadata != nil {
		if err := meta.Set(params.Metadata); err != nil {
			return domain.Invoice{}, err
		}
	}

	start := time.Now()
	row := p.pool.QueryRow(ctx, `
INSERT INTO billing_invoices (account_id, amount, currency, description, metadata, idempotency_key)
VALUES ($1, $2, $3, NULLIF($4,''), $5, $6)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING id, account_id, amount, currency, description, metadata, status, idempotency_key, created_at, updated_at, paid_at
`, params.AccountID, params.Amount.Amount, params.Amount.Currency, params.Description, meta, params.IdempotencyKey)
	invoice, err := scanInvoice(row)
	metrics.ObserveNetworkRequest("postgres", "billing_invoices_create", "billing_invoices", start, err)
	if err == nil {
		return invoice, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Invoice{}, err
	}

	invoice, err := p.GetInvoiceByIdempotencyKey(ctx, params.IdempotencyKey)
	if err != nil {
		return domain.Invoice{}, err
	}
	if invoice.AccountID != params.AccountID || invoice.Amount.Amount != params.Amount.Amount || invoice.Amount.Currency != params.Amount.Currency {
		return domain.Invoice{}, fmt.Errorf("invoice idempotency conflict")
	}
	return invoice, nil
}

func (p *Postgres) GetInvoiceByID(ctx context.Context, invoiceID int64) (domain.Invoice, error) {
	ctx, cancel := p.connCtxWithParent(ctx)
	defer cancel()

	start := time.Now()
	row := p.pool.QueryRow(ctx, `
SELECT id, account_id, amount, currency, description, metadata, status, idempotency_key, created_at, updated_at, paid_at
FROM billing_invoices
WHERE id = $1
`, invoiceID)
	invoice, err := scanInvoice(row)
	metrics.ObserveNetworkRequest("postgres", "billing_invoices_get_by_id", "billing_invoices", start, err)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Invoice{}, domain.ErrInvoiceNotFound
		}
		return domain.Invoice{}, err
	}
	return invoice, nil
}

func (p *Postgres) GetInvoiceByIdempotencyKey(ctx context.Context, key string) (domain.Invoice, error) {
	if key == "" {
		return domain.Invoice{}, fmt.Errorf("idempotency key is required")
	}
	ctx, cancel := p.connCtxWithParent(ctx)
	defer cancel()

	start := time.Now()
	row := p.pool.QueryRow(ctx, `
SELECT id, account_id, amount, currency, description, metadata, status, idempotency_key, created_at, updated_at, paid_at
FROM billing_invoices
WHERE idempotency_key = $1
`, key)
	invoice, err := scanInvoice(row)
	metrics.ObserveNetworkRequest("postgres", "billing_invoices_get_by_key", "billing_invoices", start, err)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Invoice{}, domain.ErrInvoiceNotFound
		}
		return domain.Invoice{}, err
	}
	return invoice, nil
}

func (p *Postgres) RegisterIncomingPayment(ctx context.Context, params domain.RegisterIncomingPaymentParams) (domain.Payment, error) {
	if params.IdempotencyKey == "" {
		return domain.Payment{}, fmt.Errorf("idempotency key is required")
	}
	ctx, cancel := p.connCtxWithParent(ctx)
	defer cancel()

	account, err := p.getAccount(ctx, params.AccountID)
	if err != nil {
		return domain.Payment{}, err
	}
	if params.Amount.Currency == "" {
		params.Amount.Currency = account.Balance.Currency
	}
	if account.Balance.Currency != params.Amount.Currency {
		return domain.Payment{}, fmt.Errorf("account currency mismatch")
	}

	start := time.Now()
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	metrics.ObserveNetworkRequest("postgres", "begin_tx", "billing_payments", start, err)
	if err != nil {
		return domain.Payment{}, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	accForUpdate, err := p.lockAccount(ctx, tx, params.AccountID)
	if err != nil {
		return domain.Payment{}, err
	}
	if accForUpdate.Balance.Currency != params.Amount.Currency {
		return domain.Payment{}, fmt.Errorf("account currency mismatch")
	}

	var invoice *domain.Invoice
	if params.InvoiceID != nil {
		inv, err := p.lockInvoiceForUpdate(ctx, tx, *params.InvoiceID)
		if err != nil {
			return domain.Payment{}, err
		}
		invoice = &inv
		if invoice.AccountID != params.AccountID {
			return domain.Payment{}, fmt.Errorf("invoice belongs to another account")
		}
		if invoice.Amount.Currency != params.Amount.Currency {
			return domain.Payment{}, fmt.Errorf("invoice currency mismatch")
		}
	}

	var meta pgtype.JSONB
	if params.Metadata != nil {
		if err := meta.Set(params.Metadata); err != nil {
			return domain.Payment{}, err
		}
	}

	start = time.Now()
	row := tx.QueryRow(ctx, `
INSERT INTO billing_payments (account_id, invoice_id, amount, currency, metadata, idempotency_key)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING id, account_id, invoice_id, amount, currency, metadata, status, idempotency_key, created_at, updated_at, completed_at
`, params.AccountID, params.InvoiceID, params.Amount.Amount, params.Amount.Currency, meta, params.IdempotencyKey)
	payment, scanErr := scanPayment(row)
	metrics.ObserveNetworkRequest("postgres", "billing_payments_create", "billing_payments", start, scanErr)
	if scanErr != nil {
		if !errors.Is(scanErr, pgx.ErrNoRows) {
			return domain.Payment{}, scanErr
		}
		payment, err = p.getPaymentByKey(ctx, tx, params)
		if err != nil {
			return domain.Payment{}, err
		}
		if payment.AccountID != params.AccountID || payment.Amount.Amount != params.Amount.Amount || payment.Amount.Currency != params.Amount.Currency {
			return domain.Payment{}, fmt.Errorf("payment idempotency conflict")
		}
		if params.InvoiceID != nil {
			if payment.InvoiceID == nil || *payment.InvoiceID != *params.InvoiceID {
				return domain.Payment{}, fmt.Errorf("payment invoice mismatch")
			}
		}
		start = time.Now()
		err = tx.Commit(ctx)
		metrics.ObserveNetworkRequest("postgres", "commit", "billing_payments", start, err)
		return payment, err
	}

	start = time.Now()
	_, err = tx.Exec(ctx, `
UPDATE billing_accounts
SET balance = balance + $1, updated_at = now()
WHERE id = $2
`, params.Amount.Amount, payment.AccountID)
	metrics.ObserveNetworkRequest("postgres", "billing_accounts_update_balance", "billing_accounts", start, err)
	if err != nil {
		return domain.Payment{}, err
	}

	now := time.Now()
	start = time.Now()
	_, err = tx.Exec(ctx, `
UPDATE billing_payments
SET status = 'completed', completed_at = $2, updated_at = now()
WHERE id = $1
`, payment.ID, now)
	metrics.ObserveNetworkRequest("postgres", "billing_payments_mark_completed", "billing_payments", start, err)
	if err != nil {
		if pgErr, ok := err.(*pgconn.PgError); ok {
			return domain.Payment{}, fmt.Errorf("could not mark payment completed: %w", pgErr)
		}
		return domain.Payment{}, err
	}
	payment.Status = "completed"
	payment.CompletedAt = &now
	payment.UpdatedAt = now

	if invoice != nil && invoice.Status == "pending" {
		if invoice.Amount.Amount > params.Amount.Amount {
			return domain.Payment{}, fmt.Errorf("partial payments are not supported")
		}
		start = time.Now()
		_, err = tx.Exec(ctx, `
UPDATE billing_invoices
SET status = 'paid', paid_at = $2, updated_at = now()
WHERE id = $1
`, *params.InvoiceID, now)
		metrics.ObserveNetworkRequest("postgres", "billing_invoices_mark_paid", "billing_invoices", start, err)
		if err != nil {
			return domain.Payment{}, err
		}
		payment.InvoiceID = params.InvoiceID
	}

	start = time.Now()
	err = tx.Commit(ctx)
	metrics.ObserveNetworkRequest("postgres", "commit", "billing_payments", start, err)
	return payment, err
}

func (p *Postgres) getAccount(ctx context.Context, accountID int64) (domain.BillingAccount, error) {
	start := time.Now()
	row := p.pool.QueryRow(ctx, `
SELECT id, user_id, balance, currency, created_at, updated_at
FROM billing_accounts
WHERE id = $1
`, accountID)
	acc, err := scanAccount(row)
	metrics.ObserveNetworkRequest("postgres", "billing_accounts_get", "billing_accounts", start, err)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.BillingAccount{}, fmt.Errorf("billing account not found")
		}
		return domain.BillingAccount{}, err
	}
	return acc, nil
}

func (p *Postgres) getAccountByUserID(ctx context.Context, userID int64) (domain.BillingAccount, error) {
	start := time.Now()
	row := p.pool.QueryRow(ctx, `
SELECT id, user_id, balance, currency, created_at, updated_at
FROM billing_accounts
WHERE user_id = $1
`, userID)
	acc, err := scanAccount(row)
	metrics.ObserveNetworkRequest("postgres", "billing_accounts_get_by_user_id", "billing_accounts", start, err)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.BillingAccount{}, fmt.Errorf("billing account not found")
		}
		return domain.BillingAccount{}, err
	}
	return acc, nil
}

func (p *Postgres) lockAccount(ctx context.Context, tx pgx.Tx, accountID int64) (domain.BillingAccount, error) {
	start := time.Now()
	row := tx.QueryRow(ctx, `
SELECT id, user_id, balance, currency, created_at, updated_at
FROM billing_accounts
WHERE id = $1
FOR UPDATE
`, accountID)
	acc, err := scanAccount(row)
	metrics.ObserveNetworkRequest("postgres", "billing_accounts_lock", "billing_accounts", start, err)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.BillingAccount{}, fmt.Errorf("billing account not found")
		}
		return domain.BillingAccount{}, err
	}
	return acc, nil
}

func (p *Postgres) lockInvoiceForUpdate(ctx context.Context, tx pgx.Tx, invoiceID int64) (domain.Invoice, error) {
	start := time.Now()
	row := tx.QueryRow(ctx, `
SELECT id, account_id, amount, currency, description, metadata, status, idempotency_key, created_at, updated_at, paid_at
FROM billing_invoices
WHERE id = $1
FOR UPDATE
`, invoiceID)
	invoice, err := scanInvoice(row)
	metrics.ObserveNetworkRequest("postgres", "billing_invoices_lock", "billing_invoices", start, err)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Invoice{}, domain.ErrInvoiceNotFound
		}
		return domain.Invoice{}, err
	}
	return invoice, nil
}

func (p *Postgres) getPaymentByKey(ctx context.Context, tx pgx.Tx, params domain.RegisterIncomingPaymentParams) (domain.Payment, error) {
	start := time.Now()
	row := tx.QueryRow(ctx, `
SELECT id, account_id, invoice_id, amount, currency, metadata, status, idempotency_key, created_at, updated_at, completed_at
FROM billing_payments
WHERE idempotency_key = $1
`, params.IdempotencyKey)
	payment, err := scanPayment(row)
	metrics.ObserveNetworkRequest("postgres", "billing_payments_get_by_key", "billing_payments", start, err)
	return payment, err
}

func scanAccount(row pgx.Row) (domain.BillingAccount, error) {
	var acc domain.BillingAccount
	err := row.Scan(&acc.ID, &acc.UserID, &acc.Balance.Amount, &acc.Balance.Currency, &acc.CreatedAt, &acc.UpdatedAt)
	if err != nil {
		return domain.BillingAccount{}, err
	}
	if acc.Balance.Currency == "" {
		acc.Balance.Currency = billingCurrencyDefault
	}
	return acc, nil
}

func scanInvoice(row pgx.Row) (domain.Invoice, error) {
	var inv domain.Invoice
	var metadata pgtype.JSONB
	var paidAt sql.NullTime
	err := row.Scan(&inv.ID, &inv.AccountID, &inv.Amount.Amount, &inv.Amount.Currency, &inv.Description, &metadata, &inv.Status, &inv.IdempotencyKey, &inv.CreatedAt, &inv.UpdatedAt, &paidAt)
	if err != nil {
		return domain.Invoice{}, err
	}
	if metadata.Valid {
		if err := json.Unmarshal(metadata.Bytes, &inv.Metadata); err != nil {
			return domain.Invoice{}, err
		}
	}
	if paidAt.Valid {
		t := paidAt.Time
		inv.PaidAt = &t
	}
	if inv.Amount.Currency == "" {
		inv.Amount.Currency = billingCurrencyDefault
	}
	return inv, nil
}

func scanPayment(row pgx.Row) (domain.Payment, error) {
	var pay domain.Payment
	var metadata pgtype.JSONB
	var invoiceID sql.NullInt64
	var completedAt sql.NullTime
	err := row.Scan(&pay.ID, &pay.AccountID, &invoiceID, &pay.Amount.Amount, &pay.Amount.Currency, &metadata, &pay.Status, &pay.IdempotencyKey, &pay.CreatedAt, &pay.UpdatedAt, &completedAt)
	if err != nil {
		return domain.Payment{}, err
	}
	if invoiceID.Valid {
		id := invoiceID.Int64
		pay.InvoiceID = &id
	}
	if metadata.Valid {
		if err := json.Unmarshal(metadata.Bytes, &pay.Metadata); err != nil {
			return domain.Payment{}, err
		}
	}
	if completedAt.Valid {
		t := completedAt.Time
		pay.CompletedAt = &t
	}
	if pay.Amount.Currency == "" {
		pay.Amount.Currency = billingCurrencyDefault
	}
	return pay, nil
}

func NewBillingAdapter(pool *pgxpool.Pool) domain.Billing {
	return &Postgres{pool: pool}
}
