package httpapi

import (
	"crypto/rsa"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
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
	authToken        string
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

func WithAuthToken(token string) Option {
	return func(s *Server) {
		s.authToken = token
	}
}

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// === Requests

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
	QrId           *string        `json:"qr_id"`
}

type registerPaymentRequest struct {
	AccountID      int64          `json:"account_id"`
	InvoiceID      *int64         `json:"invoice_id"`
	Amount         domain.Money   `json:"amount"`
	Metadata       map[string]any `json:"metadata"`
	IdempotencyKey string         `json:"idempotency_key"`
}

type createSBPInvoiceRequest struct {
	UserID         int64          `json:"user_id"`
	Amount         int64          `json:"amount"`
	Currency       string         `json:"currency"`
	Description    string         `json:"description"`
	PaymentPurpose string         `json:"paymentPurpose"`
	IdempotencyKey string         `json:"idempotency_key"`
	QRType         string         `json:"qrcType"`
	RedirectUrl    string         `json:"redirectUrl"`
	Metadata       map[string]any `json:"metadata"`
	Extra          map[string]any `json:"extra"`
}

// === Responses

type sbpQRCodeResponse struct {
	QRID        string         `json:"qr_id"`
	PaymentLink string         `json:"payment_link,omitempty"`
	ExpiresAt   *time.Time     `json:"expires_at,omitempty"`
	Raw         map[string]any `json:"raw,omitempty"`
}

type createSBPInvoiceResponse struct {
	Invoice domain.Invoice    `json:"invoice"`
	QR      sbpQRCodeResponse `json:"qr"`
}

// === Server

func NewServer(b domain.Billing, opts ...Option) *Server {
	srv := &Server{billing: b, log: zerolog.Nop()}
	for _, opt := range opts {
		opt(srv)
	}
	return srv
}

func (s *Server) Router() http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		if !c.Response().Committed {
			_ = c.JSON(http.StatusInternalServerError, errorResponse{
				Error: "internal server error",
				Code:  "internal_error",
			})
		}
		s.log.Error().Err(err).Msg("http server error")
	}
	e.Use(middleware.Recover())

	if s.authToken != "" {
		e.Use(s.authMiddleware)
	}

	e.GET("/swagger/openapi.yaml", s.handleSwagger)

	// Billing
	e.POST("/api/v1/accounts/ensure", s.handleEnsureAccount)
	e.GET("/api/v1/accounts/by-user/:userID", s.handleGetAccountByUserID)
	e.POST("/api/v1/accounts/charge", s.handleChargeAccount)

	e.POST("/api/v1/invoices", s.handleCreateInvoice)
	e.GET("/api/v1/invoices/:id", s.handleGetInvoiceByID)
	e.GET("/api/v1/invoices/idempotency/:key", s.handleGetInvoiceByIdempotencyKey)

	e.POST("/api/v1/payments/incoming", s.handleRegisterIncomingPayment)

	// SBP
	if s.sbpService != nil {
		e.POST("/api/v1/sbp/invoices", s.handleCreateSBPInvoice)
		e.POST("/api/v1/sbp/webhook", s.handleSBPWebhook)
	}

	return e
}

func (s *Server) authMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := c.Request()
		if req != nil && req.URL.Path == "/api/v1/sbp/webhook" {
			return next(c)
		}

		token := extractToken(req)
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.authToken)) != 1 {
			return writeError(c, http.StatusUnauthorized, "unauthorized", "invalid or missing token")
		}

		return next(c)
	}
}

func extractToken(r *http.Request) string {
	if r == nil {
		return ""
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		const bearerPrefix = "Bearer "
		if strings.HasPrefix(authHeader, bearerPrefix) {
			return strings.TrimSpace(strings.TrimPrefix(authHeader, bearerPrefix))
		}
		return strings.TrimSpace(authHeader)
	}

	token := r.Header.Get("X-API-Token")
	if token != "" {
		return strings.TrimSpace(token)
	}

	return ""
}

// === Handlers

func (s *Server) handleEnsureAccount(c echo.Context) error {
	var req ensureAccountRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid_request", "invalid request body")
	}
	if req.UserID == 0 {
		return writeError(c, http.StatusBadRequest, "invalid_request", "user_id is required")
	}
	account, err := s.billing.EnsureAccount(c.Request().Context(), req.UserID)
	if err != nil {
		return writeError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
	return writeJSON(c, http.StatusOK, account)
}

func (s *Server) handleGetAccountByUserID(c echo.Context) error {
	userIDParam := c.Param("userID")
	userID, err := strconv.ParseInt(userIDParam, 10, 64)
	if err != nil || userID == 0 {
		return writeError(c, http.StatusBadRequest, "invalid_request", "invalid user id")
	}
	account, err := s.billing.GetAccountByUserID(c.Request().Context(), userID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			return writeError(c, http.StatusNotFound, "account_not_found", "account not found")
		}
		return writeError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
	return writeJSON(c, http.StatusOK, account)
}

func (s *Server) handleChargeAccount(c echo.Context) error {
	var req chargeAccountRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid_request", "invalid request body")
	}
	if req.AccountID == 0 || req.IdempotencyKey == "" {
		return writeError(c, http.StatusBadRequest, "invalid_request", "account_id and idempotency_key are required")
	}
	payment, err := s.billing.ChargeAccount(c.Request().Context(), domain.ChargeAccountParams(req))
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInsufficientFunds):
			return writeError(c, http.StatusConflict, "insufficient_funds", err.Error())
		case errors.Is(err, domain.ErrAccountNotFound):
			return writeError(c, http.StatusNotFound, "account_not_found", "account not found")
		default:
			return writeError(c, http.StatusInternalServerError, "internal_error", err.Error())
		}
	}
	return writeJSON(c, http.StatusOK, payment)
}

func (s *Server) handleCreateInvoice(c echo.Context) error {
	var req createInvoiceRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid_request", "invalid request body")
	}
	if req.AccountID == 0 || req.IdempotencyKey == "" {
		return writeError(c, http.StatusBadRequest, "invalid_request", "account_id and idempotency_key are required")
	}
	invoice, err := s.billing.CreateInvoice(c.Request().Context(), domain.CreateInvoiceParams(req))
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAccountNotFound):
			return writeError(c, http.StatusNotFound, "account_not_found", "account not found")
		default:
			return writeError(c, http.StatusInternalServerError, "internal_error", err.Error())
		}
	}
	return writeJSON(c, http.StatusOK, invoice)
}

func (s *Server) handleGetInvoiceByID(c echo.Context) error {
	idParam := c.Param("id")
	invoiceID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil || invoiceID == 0 {
		return writeError(c, http.StatusBadRequest, "invalid_request", "invalid invoice id")
	}
	invoice, err := s.billing.GetInvoiceByID(c.Request().Context(), invoiceID)
	if err != nil {
		if errors.Is(err, domain.ErrInvoiceNotFound) {
			return writeError(c, http.StatusNotFound, "invoice_not_found", "invoice not found")
		}
		return writeError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
	return writeJSON(c, http.StatusOK, invoice)
}

func (s *Server) handleGetInvoiceByIdempotencyKey(c echo.Context) error {
	key := c.Param("key")
	if key == "" {
		return writeError(c, http.StatusBadRequest, "invalid_request", "idempotency key is required")
	}
	invoice, err := s.billing.GetInvoiceByIdempotencyKey(c.Request().Context(), key)
	if err != nil {
		if errors.Is(err, domain.ErrInvoiceNotFound) {
			return writeError(c, http.StatusNotFound, "invoice_not_found", "invoice not found")
		}
		return writeError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
	return writeJSON(c, http.StatusOK, invoice)
}

func (s *Server) handleRegisterIncomingPayment(c echo.Context) error {
	var req registerPaymentRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid_request", "invalid request body")
	}
	if req.AccountID == 0 || req.IdempotencyKey == "" {
		return writeError(c, http.StatusBadRequest, "invalid_request", "account_id and idempotency_key are required")
	}
	payment, err := s.billing.RegisterIncomingPayment(c.Request().Context(), domain.RegisterIncomingPaymentParams(req))
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvoiceNotFound):
			return writeError(c, http.StatusNotFound, "invoice_not_found", "invoice not found")
		case errors.Is(err, domain.ErrAccountNotFound):
			return writeError(c, http.StatusNotFound, "account_not_found", "account not found")
		default:
			return writeError(c, http.StatusInternalServerError, "internal_error", err.Error())
		}
	}
	return writeJSON(c, http.StatusOK, payment)
}

func (s *Server) handleCreateSBPInvoice(c echo.Context) error {
	if s.sbpService == nil {
		s.log.Error().Msg("billing: sbp service not configured")
		return writeError(c, http.StatusServiceUnavailable, "sbp_not_configured", "sbp service is not available")
	}
	var req createSBPInvoiceRequest
	if err := c.Bind(&req); err != nil {
		s.log.Error().Err(err).Msg("billing: invalid sbp invoice request body")
		return writeError(c, http.StatusBadRequest, "invalid_request", "invalid request body")
	}
	if req.UserID == 0 {
		s.log.Error().Msg("billing: sbp invoice request missing user id")
		return writeError(c, http.StatusBadRequest, "invalid_request", "user_id is required")
	}
	if req.Amount <= 0 {
		s.log.Error().Int64("user", req.UserID).Int64("amount", req.Amount).Msg("billing: sbp invoice amount must be positive")
		return writeError(c, http.StatusBadRequest, "invalid_request", "amount_minor must be positive")
	}

	params := sbpusecase.CreateInvoiceParams{
		UserID:         req.UserID,
		Amount:         domain.Money{Amount: req.Amount, Currency: req.Currency},
		Description:    req.Description,
		PaymentPurpose: req.PaymentPurpose,
		IdempotencyKey: req.IdempotencyKey,
		QRType:         req.QRType,
		RedirectUrl:    req.RedirectUrl,
		Metadata:       req.Metadata,
		Extra:          req.Extra,
	}
	result, err := s.sbpService.CreateInvoiceWithQRCode(c.Request().Context(), params)
	if err != nil {
		s.log.Error().Err(err).Msg("sbp: create invoice")
		return writeError(c, http.StatusInternalServerError, "internal_error", "failed to create invoice")
	}

	resp := createSBPInvoiceResponse{
		Invoice: result.Invoice,
		QR: sbpQRCodeResponse{
			QRID:        result.QR.QRID,
			PaymentLink: result.QR.PaymentLink,
			ExpiresAt:   result.QR.ExpiresAt,
			Raw:         result.QR.Raw,
		},
	}
	return writeJSON(c, http.StatusOK, resp)
}

func (s *Server) handleSBPWebhook(c echo.Context) error {
	if s.sbpService == nil {
		return writeError(c, http.StatusServiceUnavailable, "sbp_not_configured", "sbp service is not available")
	}
	// Простейшая защита: секрет/токен в заголовке или query
	if s.sbpWebhookSecret != "" {
		secret := c.Request().Header.Get("X-Webhook-Secret")
		if secret == "" {
			secret = c.QueryParam("token")
		}
		if secret != s.sbpWebhookSecret {
			return writeError(c, http.StatusUnauthorized, "unauthorized", "invalid webhook secret")
		}
	}

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return writeError(c, http.StatusBadRequest, "invalid_request", "failed to read body")
	}
	notification, err := tochka.ParseSbpWebhook(body, s.sbpWebhookKey)
	if err != nil {
		if errors.Is(err, tochka.ErrInvalidWebhookSignature) {
			return writeError(c, http.StatusUnauthorized, "unauthorized", "invalid webhook signature")
		}
		return writeError(c, http.StatusBadRequest, "invalid_request", "invalid webhook payload")
	}

	payment, err := s.sbpService.HandleIncomingPayment(c.Request().Context(), notification)
	if err != nil {
		if errors.Is(err, domain.ErrInvoiceNotFound) {
			return writeError(c, http.StatusNotFound, "invoice_not_found", "invoice not found")
		}
		s.log.Error().Err(err).Msg("sbp: handle webhook")
		return writeError(c, http.StatusInternalServerError, "internal_error", "failed to register payment")
	}
	return writeJSON(c, http.StatusOK, map[string]any{"status": "ok", "payment_id": payment.ID})
}

// === Helpers

func writeJSON(c echo.Context, status int, v any) error {
	return c.JSON(status, v)
}

func writeError(c echo.Context, status int, code, message string) error {
	return writeJSON(c, status, errorResponse{Error: message, Code: code})
}
