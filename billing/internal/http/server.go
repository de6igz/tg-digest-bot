package httpapi

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"billing/internal/domain"
	"billing/internal/tochka"
	sbpusecase "billing/internal/usecase/sbp"
)

type Server struct {
	billing          domain.Billing
	log              zerolog.Logger
	sbpService       *sbpusecase.Service
	sbpWebhookSecret string
	sbpWebhookKey    *rsa.PublicKey
}

type Option func(*Server)

func WithLogger(log zerolog.Logger) Option {
	return func(s *Server) {
		s.log = log
	}
}

func WithSBPService(service *sbpusecase.Service, webhookSecret string, webhookKey *rsa.PublicKey) Option {
	return func(s *Server) {
		s.sbpService = service
		s.sbpWebhookSecret = webhookSecret
		s.sbpWebhookKey = webhookKey
	}
}

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

type ensureAccountRequest struct {
	UserID int64 `json:"user_id"`
}

type chargeAccountRequest struct {
	AccountID      int64          `json:"account_id"`
	Amount         domain.Money   `json:"amount"`
	Description    string         `json:"description"`
	Metadata       map[string]any `json:"metadata"`
	IdempotencyKey string         `json:"idempotency_key"`
}

type createInvoiceRequest struct {
	AccountID      int64          `json:"account_id"`
	Amount         domain.Money   `json:"amount"`
	Description    string         `json:"description"`
	Metadata       map[string]any `json:"metadata"`
	IdempotencyKey string         `json:"idempotency_key"`
}

type registerPaymentRequest struct {
	AccountID      int64          `json:"account_id"`
	InvoiceID      *int64         `json:"invoice_id"`
	Amount         domain.Money   `json:"amount"`
	Metadata       map[string]any `json:"metadata"`
	IdempotencyKey string         `json:"idempotency_key"`
}

type createSBPInvoiceRequest struct {
	UserID          int64          `json:"user_id"`
	AmountMinor     int64          `json:"amount_minor"`
	Currency        string         `json:"currency"`
	Description     string         `json:"description"`
	PaymentPurpose  string         `json:"payment_purpose"`
	IdempotencyKey  string         `json:"idempotency_key"`
	QRType          string         `json:"qr_type"`
	NotificationURL string         `json:"notification_url"`
	Metadata        map[string]any `json:"metadata"`
	Extra           map[string]any `json:"extra"`
}

type sbpQRCodeResponse struct {
	QRID          string         `json:"qr_id"`
	PaymentLink   string         `json:"payment_link,omitempty"`
	Payload       string         `json:"payload,omitempty"`
	PayloadBase64 string         `json:"payload_base64,omitempty"`
	Status        string         `json:"status,omitempty"`
	ExpiresAt     *time.Time     `json:"expires_at,omitempty"`
	Raw           map[string]any `json:"raw,omitempty"`
}

type createSBPInvoiceResponse struct {
	Invoice domain.Invoice    `json:"invoice"`
	QR      sbpQRCodeResponse `json:"qr"`
}

func NewServer(b domain.Billing, opts ...Option) *Server {
	srv := &Server{billing: b, log: zerolog.Nop()}
	for _, opt := range opts {
		opt(srv)
	}
	return srv
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Post("/api/v1/accounts/ensure", s.handleEnsureAccount)
	r.Get("/api/v1/accounts/by-user/{userID}", s.handleGetAccountByUserID)
	r.Post("/api/v1/accounts/charge", s.handleChargeAccount)

	r.Post("/api/v1/invoices", s.handleCreateInvoice)
	r.Get("/api/v1/invoices/{id}", s.handleGetInvoiceByID)
	r.Get("/api/v1/invoices/idempotency/{key}", s.handleGetInvoiceByIdempotencyKey)

	r.Post("/api/v1/payments/incoming", s.handleRegisterIncomingPayment)

	if s.sbpService != nil {
		r.Post("/api/v1/sbp/invoices", s.handleCreateSBPInvoice)
		r.Post("/api/v1/sbp/webhook", s.handleSBPWebhook)
	}

	return r
}

func (s *Server) handleEnsureAccount(w http.ResponseWriter, r *http.Request) {
	var req ensureAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	if req.UserID == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "user_id is required")
		return
	}
	account, err := s.billing.EnsureAccount(r.Context(), req.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) handleGetAccountByUserID(w http.ResponseWriter, r *http.Request) {
	userIDParam := chi.URLParam(r, "userID")
	userID, err := strconv.ParseInt(userIDParam, 10, 64)
	if err != nil || userID == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid user id")
		return
	}
	account, err := s.billing.GetAccountByUserID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			writeError(w, http.StatusNotFound, "account_not_found", "account not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) handleChargeAccount(w http.ResponseWriter, r *http.Request) {
	var req chargeAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	if req.AccountID == 0 || req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "account_id and idempotency_key are required")
		return
	}
	payment, err := s.billing.ChargeAccount(r.Context(), domain.ChargeAccountParams(req))
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInsufficientFunds):
			writeError(w, http.StatusConflict, "insufficient_funds", err.Error())
		case errors.Is(err, domain.ErrAccountNotFound):
			writeError(w, http.StatusNotFound, "account_not_found", "account not found")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, payment)
}

func (s *Server) handleCreateInvoice(w http.ResponseWriter, r *http.Request) {
	var req createInvoiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	if req.AccountID == 0 || req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "account_id and idempotency_key are required")
		return
	}
	invoice, err := s.billing.CreateInvoice(r.Context(), domain.CreateInvoiceParams(req))
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAccountNotFound):
			writeError(w, http.StatusNotFound, "account_not_found", "account not found")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, invoice)
}

func (s *Server) handleGetInvoiceByID(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")
	invoiceID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil || invoiceID == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid invoice id")
		return
	}
	invoice, err := s.billing.GetInvoiceByID(r.Context(), invoiceID)
	if err != nil {
		if errors.Is(err, domain.ErrInvoiceNotFound) {
			writeError(w, http.StatusNotFound, "invoice_not_found", "invoice not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, invoice)
}

func (s *Server) handleGetInvoiceByIdempotencyKey(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "idempotency key is required")
		return
	}
	invoice, err := s.billing.GetInvoiceByIdempotencyKey(r.Context(), key)
	if err != nil {
		if errors.Is(err, domain.ErrInvoiceNotFound) {
			writeError(w, http.StatusNotFound, "invoice_not_found", "invoice not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, invoice)
}

func (s *Server) handleRegisterIncomingPayment(w http.ResponseWriter, r *http.Request) {
	var req registerPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	if req.AccountID == 0 || req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "account_id and idempotency_key are required")
		return
	}
	payment, err := s.billing.RegisterIncomingPayment(r.Context(), domain.RegisterIncomingPaymentParams(req))
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvoiceNotFound):
			writeError(w, http.StatusNotFound, "invoice_not_found", "invoice not found")
		case errors.Is(err, domain.ErrAccountNotFound):
			writeError(w, http.StatusNotFound, "account_not_found", "account not found")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, payment)
}

func (s *Server) handleCreateSBPInvoice(w http.ResponseWriter, r *http.Request) {
	if s.sbpService == nil {
		writeError(w, http.StatusServiceUnavailable, "sbp_not_configured", "sbp service is not available")
		return
	}
	var req createSBPInvoiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	if req.UserID == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "user_id is required")
		return
	}
	if req.AmountMinor <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "amount_minor must be positive")
		return
	}
	params := sbpusecase.CreateInvoiceParams{
		UserID:          req.UserID,
		Amount:          domain.Money{Amount: req.AmountMinor, Currency: req.Currency},
		Description:     req.Description,
		PaymentPurpose:  req.PaymentPurpose,
		IdempotencyKey:  req.IdempotencyKey,
		QRType:          req.QRType,
		NotificationURL: req.NotificationURL,
		Metadata:        req.Metadata,
		Extra:           req.Extra,
	}
	result, err := s.sbpService.CreateInvoiceWithQRCode(r.Context(), params)
	if err != nil {
		s.log.Error().Err(err).Msg("sbp: create invoice")
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create invoice")
		return
	}
	resp := createSBPInvoiceResponse{
		Invoice: result.Invoice,
		QR: sbpQRCodeResponse{
			QRID:          result.QR.QRID,
			PaymentLink:   result.QR.PaymentLink,
			Payload:       result.QR.Payload,
			PayloadBase64: result.QR.PayloadBase64,
			Status:        result.QR.Status,
			ExpiresAt:     result.QR.ExpiresAt,
			Raw:           result.QR.Raw,
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSBPWebhook(w http.ResponseWriter, r *http.Request) {
	if s.sbpService == nil {
		writeError(w, http.StatusServiceUnavailable, "sbp_not_configured", "sbp service is not available")
		return
	}
	if s.sbpWebhookSecret != "" {
		secret := r.Header.Get("X-Webhook-Secret")
		if secret == "" {
			secret = r.URL.Query().Get("token")
		}
		if secret != s.sbpWebhookSecret {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid webhook secret")
			return
		}
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "failed to read body")
		return
	}
	notification, err := tochka.ParseSbpWebhook(body, s.sbpWebhookKey)
	if err != nil {
		if errors.Is(err, tochka.ErrInvalidWebhookSignature) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid webhook signature")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid webhook payload")
		return
	}
	payment, err := s.sbpService.HandleIncomingPayment(r.Context(), notification)
	if err != nil {
		if errors.Is(err, domain.ErrInvoiceNotFound) {
			writeError(w, http.StatusNotFound, "invoice_not_found", "invoice not found")
			return
		}
		s.log.Error().Err(err).Msg("sbp: handle webhook")
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to register payment")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "payment_id": payment.ID})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: message, Code: code})
}
