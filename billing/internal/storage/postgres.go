package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"billing/internal/domain"
)

const (
	billingCurrencyDefault = "RUB"
	queryTimeout           = 5 * time.Second
)

type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

func (p *Postgres) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), queryTimeout)
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, queryTimeout)
}

func ensureBillingAccountTx(ctx context.Context, tx pgx.Tx, userID int64) (domain.BillingAccount, error) {
	var acc domain.BillingAccount
	err := tx.QueryRow(ctx, `
INSERT INTO billing_accounts (user_id)
VALUES ($1)
ON CONFLICT (user_id) DO UPDATE SET updated_at = now()
RETURNING id, user_id, balance, currency, created_at, updated_at
`, userID).Scan(&acc.ID, &acc.UserID, &acc.Balance.Amount, &acc.Balance.Currency, &acc.CreatedAt, &acc.UpdatedAt)
	if err != nil {
		return domain.BillingAccount{}, err
	}
	if acc.Balance.Currency == "" {
		acc.Balance.Currency = billingCurrencyDefault
	}
	return acc, nil
}

func (p *Postgres) EnsureAccount(ctx context.Context, userID int64) (domain.BillingAccount, error) {
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.BillingAccount{}, err
	}
	acc, err := ensureBillingAccountTx(ctx, tx, userID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return domain.BillingAccount{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.BillingAccount{}, err
	}
	return acc, nil
}

func (p *Postgres) GetAccountByUserID(ctx context.Context, userID int64) (domain.BillingAccount, error) {
	ctx, cancel := p.withTimeout(ctx)
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
	ctx, cancel := p.withTimeout(ctx)
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

	var meta []byte
	if params.Metadata != nil {
		meta, err = json.Marshal(params.Metadata)
		if err != nil {
			return domain.Invoice{}, err
		}
	}

	row := p.pool.QueryRow(ctx, `
INSERT INTO billing_invoices (account_id, amount, currency, description, metadata, idempotency_key,qr_id)
VALUES ($1, $2, $3, NULLIF($4,''), $5, $6,$7)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING id, account_id, amount, currency, description, metadata, status, idempotency_key,qr_id, created_at, updated_at, paid_at
`, params.AccountID, params.Amount.Amount, params.Amount.Currency, params.Description, meta, params.IdempotencyKey, params.QrId)
	invoice, err := scanInvoice(row)
	if err == nil {
		return invoice, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Invoice{}, err
	}

	invoice, err = p.GetInvoiceByIdempotencyKey(ctx, params.IdempotencyKey)
	if err != nil {
		return domain.Invoice{}, err
	}
	if invoice.AccountID != params.AccountID || invoice.Amount.Amount != params.Amount.Amount || invoice.Amount.Currency != params.Amount.Currency {
		return domain.Invoice{}, fmt.Errorf("invoice idempotency conflict")
	}
	return invoice, nil
}

func (p *Postgres) GetInvoiceByID(ctx context.Context, invoiceID int64) (domain.Invoice, error) {
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	row := p.pool.QueryRow(ctx, `
SELECT id, account_id, amount, currency, description, metadata, status, idempotency_key, created_at, updated_at, paid_at
FROM billing_invoices
WHERE id = $1
`, invoiceID)
	invoice, err := scanInvoice(row)
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
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	row := p.pool.QueryRow(ctx, `
SELECT id, account_id, amount, currency, description, metadata, status, idempotency_key, created_at, updated_at, paid_at, qr_id
FROM billing_invoices
WHERE idempotency_key = $1
`, key)
	invoice, err := scanInvoice(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Invoice{}, domain.ErrInvoiceNotFound
		}
		return domain.Invoice{}, err
	}
	return invoice, nil
}

func (p *Postgres) GetInvoiceByQrId(ctx context.Context, qrId string) (domain.Invoice, error) {
	if qrId == "" {
		return domain.Invoice{}, fmt.Errorf("qrId is required")
	}
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	row := p.pool.QueryRow(ctx, `
SELECT id, account_id, amount, currency, description, metadata, status, idempotency_key, created_at, updated_at, paid_at,qr_id
FROM billing_invoices
WHERE qr_id = $1
`, qrId)
	invoice, err := scanInvoice(row)
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
	if params.Amount.Amount <= 0 {
		return domain.Payment{}, fmt.Errorf("amount must be positive")
	}
	ctx, cancel := p.withTimeout(ctx)
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

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
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

	var meta []byte
	if params.Metadata != nil {
		meta, err = json.Marshal(params.Metadata)
		if err != nil {
			return domain.Payment{}, err
		}
	}

	row := tx.QueryRow(ctx, `
INSERT INTO billing_payments (account_id, invoice_id, amount, currency, metadata, idempotency_key)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING id, account_id, invoice_id, amount, currency, metadata, status, idempotency_key, created_at, updated_at, completed_at
`, params.AccountID, params.InvoiceID, params.Amount.Amount, params.Amount.Currency, meta, params.IdempotencyKey)
	payment, scanErr := scanPayment(row)
	if scanErr != nil {
		if !errors.Is(scanErr, pgx.ErrNoRows) {
			return domain.Payment{}, scanErr
		}
		payment, err = p.getPaymentByIdempotencyKey(ctx, tx, params.IdempotencyKey)
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
		if err := tx.Commit(ctx); err != nil {
			return domain.Payment{}, err
		}
		return payment, nil
	}

	_, err = tx.Exec(ctx, `
UPDATE billing_accounts
SET balance = balance + $1, updated_at = now()
WHERE id = $2
`, params.Amount.Amount, payment.AccountID)
	if err != nil {
		return domain.Payment{}, err
	}

	now := time.Now()
	_, err = tx.Exec(ctx, `
UPDATE billing_payments
SET status = 'completed', completed_at = $2, updated_at = now()
WHERE id = $1
`, payment.ID, now)
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
		_, err = tx.Exec(ctx, `
UPDATE billing_invoices
SET status = 'paid', paid_at = $2, updated_at = now()
WHERE id = $1
`, *params.InvoiceID, now)
		if err != nil {
			return domain.Payment{}, err
		}
		payment.InvoiceID = params.InvoiceID
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Payment{}, err
	}
	return payment, nil
}

func (p *Postgres) ChargeAccount(ctx context.Context, params domain.ChargeAccountParams) (domain.Payment, error) {
	if params.IdempotencyKey == "" {
		return domain.Payment{}, fmt.Errorf("idempotency key is required")
	}
	if params.Amount.Amount == 0 {
		return domain.Payment{}, fmt.Errorf("amount must not be zero")
	}
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	account, err := p.getAccount(ctx, params.AccountID)
	if err != nil {
		return domain.Payment{}, err
	}
	if params.Amount.Currency == "" {
		params.Amount.Currency = account.Balance.Currency
	}
	if params.Amount.Currency != account.Balance.Currency {
		return domain.Payment{}, fmt.Errorf("account currency mismatch")
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Payment{}, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	lockedAccount, err := p.lockAccount(ctx, tx, params.AccountID)
	if err != nil {
		return domain.Payment{}, err
	}
	if lockedAccount.Balance.Currency != params.Amount.Currency {
		return domain.Payment{}, fmt.Errorf("account currency mismatch")
	}
	if lockedAccount.Balance.Amount < params.Amount.Amount {
		return domain.Payment{}, domain.ErrInsufficientFunds
	}

	var meta []byte
	if params.Metadata != nil {
		meta, err = json.Marshal(params.Metadata)
		if err != nil {
			return domain.Payment{}, err
		}
	}

	row := tx.QueryRow(ctx, `
INSERT INTO billing_payments (account_id, amount, currency, metadata, idempotency_key, description, status)
VALUES ($1, -$2, $3, $4, $5, NULLIF($6,''), 'completed')
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING id, account_id, invoice_id, amount, currency, metadata, status, idempotency_key, created_at, updated_at, completed_at
`, params.AccountID, params.Amount.Amount, params.Amount.Currency, meta, params.IdempotencyKey, params.Description)
	payment, scanErr := scanPayment(row)
	if scanErr != nil {
		if !errors.Is(scanErr, pgx.ErrNoRows) {
			return domain.Payment{}, scanErr
		}
		payment, err = p.getPaymentByIdempotencyKey(ctx, tx, params.IdempotencyKey)
		if err != nil {
			return domain.Payment{}, err
		}
		if payment.AccountID != params.AccountID || payment.Amount.Amount != -params.Amount.Amount || payment.Amount.Currency != params.Amount.Currency {
			return domain.Payment{}, fmt.Errorf("payment idempotency conflict")
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.Payment{}, err
		}
		return payment, nil
	}

	_, err = tx.Exec(ctx, `
UPDATE billing_accounts
SET balance = balance - $1, updated_at = now()
WHERE id = $2
`, params.Amount.Amount, params.AccountID)
	if err != nil {
		return domain.Payment{}, err
	}

	now := time.Now()
	payment.CompletedAt = &now
	payment.UpdatedAt = now

	if err := tx.Commit(ctx); err != nil {
		return domain.Payment{}, err
	}
	return payment, nil
}

func (p *Postgres) getAccount(ctx context.Context, accountID int64) (domain.BillingAccount, error) {
	row := p.pool.QueryRow(ctx, `
SELECT id, user_id, balance, currency, created_at, updated_at
FROM billing_accounts
WHERE id = $1
`, accountID)
	acc, err := scanAccount(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.BillingAccount{}, domain.ErrAccountNotFound
		}
		return domain.BillingAccount{}, err
	}
	return acc, nil
}

func (p *Postgres) getAccountByUserID(ctx context.Context, userID int64) (domain.BillingAccount, error) {
	row := p.pool.QueryRow(ctx, `
SELECT id, user_id, balance, currency, created_at, updated_at
FROM billing_accounts
WHERE user_id = $1
`, userID)
	acc, err := scanAccount(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.BillingAccount{}, domain.ErrAccountNotFound
		}
		return domain.BillingAccount{}, err
	}
	return acc, nil
}

func (p *Postgres) lockAccount(ctx context.Context, tx pgx.Tx, accountID int64) (domain.BillingAccount, error) {
	row := tx.QueryRow(ctx, `
SELECT id, user_id, balance, currency, created_at, updated_at
FROM billing_accounts
WHERE id = $1
FOR UPDATE
`, accountID)
	acc, err := scanAccount(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.BillingAccount{}, domain.ErrAccountNotFound
		}
		return domain.BillingAccount{}, err
	}
	return acc, nil
}

func (p *Postgres) lockInvoiceForUpdate(ctx context.Context, tx pgx.Tx, invoiceID int64) (domain.Invoice, error) {
	row := tx.QueryRow(ctx, `
SELECT id, account_id, amount, currency, description, metadata, status, idempotency_key, created_at, updated_at, paid_at, qr_id
FROM billing_invoices
WHERE id = $1
FOR UPDATE
`, invoiceID)
	invoice, err := scanInvoice(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Invoice{}, domain.ErrInvoiceNotFound
		}
		return domain.Invoice{}, err
	}
	return invoice, nil
}

func (p *Postgres) getPaymentByIdempotencyKey(ctx context.Context, tx pgx.Tx, key string) (domain.Payment, error) {
	row := tx.QueryRow(ctx, `
SELECT id, account_id, invoice_id, amount, currency, metadata, status, idempotency_key, created_at, updated_at, completed_at
FROM billing_payments
WHERE idempotency_key = $1
`, key)
	payment, err := scanPayment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Payment{}, fmt.Errorf("payment not found")
		}
		return domain.Payment{}, err
	}
	return payment, nil
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
	var (
		invoice  domain.Invoice
		metadata sql.NullString
	)
	err := row.Scan(&invoice.ID, &invoice.AccountID, &invoice.Amount.Amount, &invoice.Amount.Currency, &invoice.Description, &metadata, &invoice.Status, &invoice.IdempotencyKey, &invoice.CreatedAt, &invoice.UpdatedAt, &invoice.PaidAt, &invoice.QrId)
	if err != nil {
		return domain.Invoice{}, err
	}
	if invoice.Amount.Currency == "" {
		invoice.Amount.Currency = billingCurrencyDefault
	}
	if metadata.Valid && metadata.String != "" {
		if err := json.Unmarshal([]byte(metadata.String), &invoice.Metadata); err != nil {
			return domain.Invoice{}, err
		}
	}
	return invoice, nil
}

func scanPayment(row pgx.Row) (domain.Payment, error) {
	var (
		payment     domain.Payment
		metadata    sql.NullString
		invoiceID   sql.NullInt64
		completedAt sql.NullTime
	)
	err := row.Scan(&payment.ID, &payment.AccountID, &invoiceID, &payment.Amount.Amount, &payment.Amount.Currency, &metadata, &payment.Status, &payment.IdempotencyKey, &payment.CreatedAt, &payment.UpdatedAt, &completedAt)
	if err != nil {
		return domain.Payment{}, err
	}
	if payment.Amount.Currency == "" {
		payment.Amount.Currency = billingCurrencyDefault
	}
	if invoiceID.Valid {
		id := invoiceID.Int64
		payment.InvoiceID = &id
	}
	if metadata.Valid && metadata.String != "" {
		if err := json.Unmarshal([]byte(metadata.String), &payment.Metadata); err != nil {
			return domain.Payment{}, err
		}
	}
	if completedAt.Valid {
		ts := completedAt.Time
		payment.CompletedAt = &ts
	}
	return payment, nil
}

var _ domain.Billing = (*Postgres)(nil)
