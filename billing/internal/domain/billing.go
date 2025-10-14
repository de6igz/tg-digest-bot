package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvoiceNotFound   = errors.New("invoice not found")
	ErrAccountNotFound   = errors.New("account not found")
	ErrInsufficientFunds = errors.New("insufficient funds")
)

type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type BillingAccount struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Balance   Money     `json:"balance"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Invoice struct {
	ID             int64          `json:"id"`
	AccountID      int64          `json:"account_id"`
	Amount         Money          `json:"amount"`
	Description    string         `json:"description"`
	Metadata       map[string]any `json:"metadata"`
	Status         string         `json:"status"`
	IdempotencyKey string         `json:"idempotency_key"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	PaidAt         *time.Time     `json:"paid_at"`
}

type InvoiceSBPMetadata struct {
	Provider      string         `json:"provider"`
	QRID          string         `json:"qr_id,omitempty"`
	PaymentLink   string         `json:"payment_link,omitempty"`
	Payload       string         `json:"payload,omitempty"`
	PayloadBase64 string         `json:"payload_base64,omitempty"`
	ExpiresAt     *time.Time     `json:"expires_at,omitempty"`
	ProviderData  map[string]any `json:"provider_data,omitempty"`
	Extra         map[string]any `json:"extra,omitempty"`
}

type Payment struct {
	ID             int64          `json:"id"`
	AccountID      int64          `json:"account_id"`
	InvoiceID      *int64         `json:"invoice_id"`
	Amount         Money          `json:"amount"`
	Metadata       map[string]any `json:"metadata"`
	Status         string         `json:"status"`
	IdempotencyKey string         `json:"idempotency_key"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	CompletedAt    *time.Time     `json:"completed_at"`
}

type CreateInvoiceParams struct {
	AccountID      int64          `json:"account_id"`
	Amount         Money          `json:"amount"`
	Description    string         `json:"description"`
	Metadata       map[string]any `json:"metadata"`
	IdempotencyKey string         `json:"idempotency_key"`
}

type RegisterIncomingPaymentParams struct {
	AccountID      int64          `json:"account_id"`
	InvoiceID      *int64         `json:"invoice_id"`
	Amount         Money          `json:"amount"`
	Metadata       map[string]any `json:"metadata"`
	IdempotencyKey string         `json:"idempotency_key"`
}

type ChargeAccountParams struct {
	AccountID      int64          `json:"account_id"`
	Amount         Money          `json:"amount"`
	Description    string         `json:"description"`
	Metadata       map[string]any `json:"metadata"`
	IdempotencyKey string         `json:"idempotency_key"`
}

type Billing interface {
	EnsureAccount(ctx context.Context, userID int64) (BillingAccount, error)
	GetAccountByUserID(ctx context.Context, userID int64) (BillingAccount, error)
	CreateInvoice(ctx context.Context, params CreateInvoiceParams) (Invoice, error)
	RegisterIncomingPayment(ctx context.Context, params RegisterIncomingPaymentParams) (Payment, error)
	GetInvoiceByID(ctx context.Context, invoiceID int64) (Invoice, error)
	GetInvoiceByIdempotencyKey(ctx context.Context, key string) (Invoice, error)
	ChargeAccount(ctx context.Context, params ChargeAccountParams) (Payment, error)
}

func ExtractInvoiceSBPMetadata(meta map[string]any) (InvoiceSBPMetadata, bool) {
	if meta == nil {
		return InvoiceSBPMetadata{}, false
	}
	value, ok := meta["sbp"]
	if !ok {
		return InvoiceSBPMetadata{}, false
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return InvoiceSBPMetadata{}, false
	}
	var sbp InvoiceSBPMetadata
	if err := json.Unmarshal(raw, &sbp); err != nil {
		return InvoiceSBPMetadata{}, false
	}
	return sbp, true
}

func SetInvoiceSBPMetadata(meta map[string]any, sbp InvoiceSBPMetadata) map[string]any {
	if meta == nil {
		meta = make(map[string]any, 1)
	} else {
		dup := make(map[string]any, len(meta)+1)
		for k, v := range meta {
			dup[k] = v
		}
		meta = dup
	}
	meta["sbp"] = sbp
	return meta
}
