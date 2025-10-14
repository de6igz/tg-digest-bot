package sbp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"billing/internal/domain"
	"billing/internal/tochka"
)

type Client interface {
	RegisterQRCode(ctx context.Context, req tochka.RegisterQRCodeRequest) (tochka.RegisterQRCodeResponse, error)
}

type Service struct {
	billing          domain.Billing
	client           Client
	defaultNotifyURL string
	log              zerolog.Logger
}

type CreateInvoiceParams struct {
	UserID          int64
	Amount          domain.Money
	Description     string
	PaymentPurpose  string
	IdempotencyKey  string
	QRType          string
	NotificationURL string
	Metadata        map[string]any
	Extra           map[string]any
}

type CreateInvoiceResult struct {
	Invoice domain.Invoice
	QR      tochka.RegisterQRCodeResponse
}

func NewService(b domain.Billing, client Client, notificationURL string, log zerolog.Logger) *Service {
	return &Service{billing: b, client: client, defaultNotifyURL: notificationURL, log: log}
}

func (s *Service) CreateInvoiceWithQRCode(ctx context.Context, params CreateInvoiceParams) (CreateInvoiceResult, error) {
	if params.UserID == 0 {
		return CreateInvoiceResult{}, fmt.Errorf("user id is required")
	}
	if params.Amount.Amount <= 0 {
		return CreateInvoiceResult{}, fmt.Errorf("amount must be positive")
	}
	if params.IdempotencyKey == "" {
		params.IdempotencyKey = uuid.NewString()
	}
	if params.NotificationURL == "" {
		params.NotificationURL = s.defaultNotifyURL
	}
	existing, err := s.billing.GetInvoiceByIdempotencyKey(ctx, params.IdempotencyKey)
	if err == nil {
		sbpMeta, ok := domain.ExtractInvoiceSBPMetadata(existing.Metadata)
		if ok {
			var status string
			if sbpMeta.Extra != nil {
				status = fmt.Sprint(sbpMeta.Extra["status"])
			}
			return CreateInvoiceResult{
				Invoice: existing,
				QR: tochka.RegisterQRCodeResponse{
					QRID:          sbpMeta.QRID,
					PaymentLink:   sbpMeta.PaymentLink,
					Payload:       sbpMeta.Payload,
					PayloadBase64: sbpMeta.PayloadBase64,
					ExpiresAt:     sbpMeta.ExpiresAt,
					Raw:           sbpMeta.ProviderData,
					Status:        status,
				},
			}, nil
		}
		return CreateInvoiceResult{Invoice: existing}, nil
	}
	if err != nil && !errors.Is(err, domain.ErrInvoiceNotFound) {
		return CreateInvoiceResult{}, fmt.Errorf("get invoice by key: %w", err)
	}

	account, err := s.billing.EnsureAccount(ctx, params.UserID)
	if err != nil {
		return CreateInvoiceResult{}, fmt.Errorf("ensure account: %w", err)
	}
	if params.Amount.Currency == "" {
		params.Amount.Currency = account.Balance.Currency
	}
	qrRequest := tochka.RegisterQRCodeRequest{
		Amount:          params.Amount,
		Description:     params.Description,
		PaymentPurpose:  params.PaymentPurpose,
		QRType:          params.QRType,
		IdempotencyKey:  params.IdempotencyKey,
		NotificationURL: params.NotificationURL,
		Extra:           params.Extra,
	}
	qrResponse, err := s.client.RegisterQRCode(ctx, qrRequest)
	if err != nil {
		return CreateInvoiceResult{}, fmt.Errorf("register qr code: %w", err)
	}

	sbpMeta := domain.InvoiceSBPMetadata{
		Provider:      "tochka",
		QRID:          qrResponse.QRID,
		PaymentLink:   qrResponse.PaymentLink,
		Payload:       qrResponse.Payload,
		PayloadBase64: qrResponse.PayloadBase64,
		ExpiresAt:     qrResponse.ExpiresAt,
		ProviderData:  qrResponse.Raw,
		Extra: map[string]any{
			"status":          qrResponse.Status,
			"notification":    params.NotificationURL,
			"payment_purpose": params.PaymentPurpose,
		},
	}
	metadata := domain.SetInvoiceSBPMetadata(params.Metadata, sbpMeta)
	invoice, err := s.billing.CreateInvoice(ctx, domain.CreateInvoiceParams{
		AccountID:      account.ID,
		Amount:         params.Amount,
		Description:    params.Description,
		Metadata:       metadata,
		IdempotencyKey: params.IdempotencyKey,
	})
	if err != nil {
		return CreateInvoiceResult{}, fmt.Errorf("create invoice: %w", err)
	}

	return CreateInvoiceResult{Invoice: invoice, QR: qrResponse}, nil
}

func (s *Service) HandleIncomingPayment(ctx context.Context, notification tochka.IncomingPaymentNotification) (domain.Payment, error) {
	if notification.QRID == "" {
		return domain.Payment{}, fmt.Errorf("webhook missing qr id")
	}
	invoice, err := s.billing.GetInvoiceByIdempotencyKey(ctx, notification.QRID)
	if err != nil {
		return domain.Payment{}, fmt.Errorf("invoice lookup: %w", err)
	}
	amountMinor, err := notification.AmountMinor()
	if err != nil {
		return domain.Payment{}, fmt.Errorf("parse amount: %w", err)
	}
	currency := notification.Amount.Currency
	if currency == "" {
		currency = invoice.Amount.Currency
	}
	metadata := map[string]any{
		"provider":        "tochka",
		"event":           notification.Event,
		"qr_id":           notification.QRID,
		"status":          notification.Status,
		"payload":         notification.Payload,
		"payment_purpose": notification.PaymentPurpose,
		"payer_name":      notification.PayerName,
		"payer_inn":       notification.PayerINN,
		"payer_account":   notification.PayerAccount,
		"payer_bank_name": notification.PayerBankName,
	}
	if notification.OrderID != "" {
		metadata["order_id"] = notification.OrderID
	}
	metadata["raw"] = notification.Metadata()
	if notification.PaymentDate != nil {
		metadata["payment_date"] = notification.PaymentDate
	}

	idempotencyKey := notification.IdempotencyKey()
	payment, err := s.billing.RegisterIncomingPayment(ctx, domain.RegisterIncomingPaymentParams{
		AccountID: invoice.AccountID,
		InvoiceID: &invoice.ID,
		Amount: domain.Money{
			Amount:   amountMinor,
			Currency: currency,
		},
		Metadata:       metadata,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return domain.Payment{}, fmt.Errorf("register payment: %w", err)
	}
	return payment, nil
}
