package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	chi "github.com/go-chi/chi/v5"

	"billing/internal/domain"
)

type Server struct {
	billing domain.Billing
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

func NewServer(b domain.Billing) *Server {
	return &Server{billing: b}
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: message, Code: code})
}
