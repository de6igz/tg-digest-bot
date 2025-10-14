package domain

import (
	"context"
	"time"
)

type BillingSBP interface {
	CreateInvoiceWithQRCode(ctx context.Context, params CreateSBPInvoiceParams) (CreateSBPInvoiceResult, error)
}

type CreateSBPInvoiceParams struct {
	UserID          int64          `json:"user_id"`
	Amount          Money          `json:"amount"`
	Description     string         `json:"description"`
	PaymentPurpose  string         `json:"payment_purpose"`
	IdempotencyKey  string         `json:"idempotency_key"`
	QRType          string         `json:"qr_type"`
	NotificationURL string         `json:"notification_url"`
	Metadata        map[string]any `json:"metadata"`
	Extra           map[string]any `json:"extra"`
}

type CreateSBPInvoiceResult struct {
	Invoice Invoice   `json:"invoice"`
	QR      SBPQRCode `json:"qr"`
}

type SBPQRCode struct {
	QRID          string         `json:"qr_id"`
	PaymentLink   string         `json:"payment_link,omitempty"`
	Payload       string         `json:"payload,omitempty"`
	PayloadBase64 string         `json:"payload_base64,omitempty"`
	Status        string         `json:"status,omitempty"`
	ExpiresAt     *time.Time     `json:"expires_at,omitempty"`
	Raw           map[string]any `json:"raw,omitempty"`
}
