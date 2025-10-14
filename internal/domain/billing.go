package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	// ErrInvoiceNotFound возвращается, когда счёт не найден.
	ErrInvoiceNotFound = errors.New("invoice not found")

	// ErrAccountNotFound возвращается, когда счёт пользователя не найден.
	ErrAccountNotFound = errors.New("account not found")

	// ErrInsufficientFunds возвращается, когда на счёте недостаточно средств.
	ErrInsufficientFunds = errors.New("insufficient funds")
)

// Money описывает сумму в минимальных единицах валюты.
type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// BillingAccount представляет баланс пользователя.
type BillingAccount struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Balance   Money     `json:"balance"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Invoice описывает счёт на оплату.
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

// InvoiceSBPMetadata хранит информацию о QR-коде СБП, связанной со счётом.
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

// Payment описывает входящий платёж.
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

// CreateInvoiceParams содержит параметры создания счёта.
type CreateInvoiceParams struct {
	AccountID      int64          `json:"account_id"`
	Amount         Money          `json:"amount"`
	Description    string         `json:"description"`
	Metadata       map[string]any `json:"metadata"`
	IdempotencyKey string         `json:"idempotency_key"`
}

// RegisterIncomingPaymentParams содержит параметры регистрации входящего платежа.
type RegisterIncomingPaymentParams struct {
	AccountID      int64          `json:"account_id"`
	InvoiceID      *int64         `json:"invoice_id"`
	Amount         Money          `json:"amount"`
	Metadata       map[string]any `json:"metadata"`
	IdempotencyKey string         `json:"idempotency_key"`
}

// ChargeAccountParams содержит параметры списания средств.
type ChargeAccountParams struct {
	AccountID      int64          `json:"account_id"`
	Amount         Money          `json:"amount"`
	Description    string         `json:"description"`
	Metadata       map[string]any `json:"metadata"`
	IdempotencyKey string         `json:"idempotency_key"`
}

// Billing определяет контракт внутреннего биллинга.
type Billing interface {
	EnsureAccount(ctx context.Context, userID int64) (BillingAccount, error)
	GetAccountByUserID(ctx context.Context, userID int64) (BillingAccount, error)
	CreateInvoice(ctx context.Context, params CreateInvoiceParams) (Invoice, error)
	RegisterIncomingPayment(ctx context.Context, params RegisterIncomingPaymentParams) (Payment, error)
	GetInvoiceByID(ctx context.Context, invoiceID int64) (Invoice, error)
	GetInvoiceByIdempotencyKey(ctx context.Context, key string) (Invoice, error)
	ChargeAccount(ctx context.Context, params ChargeAccountParams) (Payment, error)
}

// ExtractInvoiceSBPMetadata извлекает информацию о QR-коде СБП из метаданных счёта.
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

// SetInvoiceSBPMetadata добавляет метаданные QR-кода СБП в счёт.
func SetInvoiceSBPMetadata(meta map[string]any, sbp InvoiceSBPMetadata) map[string]any {
	if meta == nil {
		meta = make(map[string]any, 1)
	} else {
		// создаём копию, чтобы не модифицировать исходную карту вызывающего
		dup := make(map[string]any, len(meta)+1)
		for k, v := range meta {
			dup[k] = v
		}
		meta = dup
	}
	meta["sbp"] = sbp
	return meta
}
