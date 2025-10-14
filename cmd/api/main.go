package main

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"

	"tg-digest-bot/internal/adapters/repo"
	"tg-digest-bot/internal/adapters/tochka"
	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/config"
	"tg-digest-bot/internal/infra/db"
	httpinfra "tg-digest-bot/internal/infra/http"
	"tg-digest-bot/internal/infra/metrics"
	billingusecase "tg-digest-bot/internal/usecase/billing"
)

func main() {
	cfg := config.Load()

	metrics.MustRegister(prometheus.DefaultRegisterer)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := db.Connect(cfg.PGDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("api: нет подключения к БД")
	}
	defer pool.Close()

	repoAdapter := repo.NewPostgres(pool)
	tochkaClient := tochka.NewClient(tochka.Config{
		BaseURL:     cfg.Tochka.BaseURL,
		MerchantID:  cfg.Tochka.MerchantID,
		AccountID:   cfg.Tochka.AccountID,
		AccessToken: cfg.Tochka.AccessToken,
		Timeout:     cfg.Tochka.Timeout,
	})
	var webhookPublicKey *rsa.PublicKey
	if cfg.Tochka.WebhookKey != "" {
		key, err := tochka.ParseRSAPublicKeyFromJWK([]byte(cfg.Tochka.WebhookKey))
		if err != nil {
			log.Fatal().Err(err).Msg("api: invalid tochka webhook public key")
		}
		webhookPublicKey = key
	}
	sbpService := billingusecase.NewService(repoAdapter, tochkaClient, log.With().Str("component", "billing_sbp").Logger())

	r := chi.NewRouter()

	r.Group(func(protected chi.Router) {
		protected.Use(httpinfra.WebAppAuthMiddleware(cfg.Telegram.Token))

		protected.Get("/api/v1/digest/today", func(w http.ResponseWriter, r *http.Request) {
			resp := map[string]any{
				"date":  time.Now().Format("2006-01-02"),
				"items": []map[string]string{},
			}
			writeJSON(w, resp)
		})

		protected.Get("/api/v1/digest/history", func(w http.ResponseWriter, r *http.Request) {
			resp := map[string]any{"history": []any{}}
			writeJSON(w, resp)
		})

		protected.Get("/api/v1/channels", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, []any{})
		})

		protected.Post("/api/v1/channels", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, map[string]string{"status": "ok"})
		})

		protected.Delete("/api/v1/channels/{id}", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})

		protected.Put("/api/v1/settings/time", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, map[string]string{"status": "ok"})
		})

		protected.Post("/api/v1/billing/sbp/invoices", func(w http.ResponseWriter, r *http.Request) {
			defer r.Body.Close()
			var req createInvoiceRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if req.UserID == 0 {
				writeError(w, http.StatusBadRequest, "user_id is required")
				return
			}
			if req.AmountMinor <= 0 {
				writeError(w, http.StatusBadRequest, "amount_minor must be positive")
				return
			}
			params := billingusecase.CreateSBPInvoiceParams{
				UserID:          req.UserID,
				Amount:          domain.Money{Amount: req.AmountMinor, Currency: req.Currency},
				Description:     req.Description,
				PaymentPurpose:  req.PaymentPurpose,
				IdempotencyKey:  req.IdempotencyKey,
				OrderID:         req.OrderID,
				QRType:          req.QRType,
				NotificationURL: req.NotificationURL,
				Metadata:        req.Metadata,
				Extra:           req.Extra,
			}
			if params.NotificationURL == "" {
				params.NotificationURL = cfg.Tochka.NotificationURL
			}
			result, err := sbpService.CreateInvoiceWithQRCode(r.Context(), params)
			if err != nil {
				log.Error().Err(err).Msg("billing: create sbp invoice")
				writeError(w, http.StatusInternalServerError, "failed to create invoice")
				return
			}
			writeJSON(w, map[string]any{
				"invoice": result.Invoice,
				"qr":      result.QR,
			})
		})
	})

	r.Post("/api/v1/billing/sbp/webhook", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if cfg.Tochka.WebhookSecret != "" {
			secret := r.Header.Get("X-Webhook-Secret")
			if secret == "" {
				secret = r.URL.Query().Get("token")
			}
			if secret != cfg.Tochka.WebhookSecret {
				writeError(w, http.StatusUnauthorized, "invalid webhook secret")
				return
			}
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read body")
			return
		}
		var notification tochka.IncomingPaymentNotification
		if webhookPublicKey != nil {
			notification, err = tochka.ParseIncomingPaymentNotificationJWT(body, webhookPublicKey)
		} else {
			notification, err = tochka.ParseIncomingPaymentNotification(body)
		}
		if err != nil {
			if errors.Is(err, tochka.ErrInvalidWebhookSignature) {
				writeError(w, http.StatusUnauthorized, "invalid webhook signature")
				return
			}
			writeError(w, http.StatusBadRequest, "invalid webhook payload")
			return
		}
		payment, err := sbpService.HandleIncomingPayment(r.Context(), notification)
		if err != nil {
			if errors.Is(err, domain.ErrInvoiceNotFound) {
				writeError(w, http.StatusNotFound, "invoice not found")
				return
			}
			log.Error().Err(err).Msg("billing: handle sbp webhook")
			writeError(w, http.StatusInternalServerError, "failed to register payment")
			return
		}
		writeJSON(w, map[string]any{
			"status":     "ok",
			"payment_id": payment.ID,
		})
	})

	srv := &http.Server{Addr: ":8080", Handler: r}
	metrics.StartServer(ctx, log.With().Str("component", "metrics").Logger(), ":9090")
	go func() {
		log.Info().Msg("api: старт")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("api: сервер остановлен")
		}
	}()
	<-ctx.Done()
	log.Info().Msg("api: остановка")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)

	_ = repoAdapter
	_ = sbpService
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type createInvoiceRequest struct {
	UserID          int64          `json:"user_id"`
	AmountMinor     int64          `json:"amount_minor"`
	Currency        string         `json:"currency"`
	Description     string         `json:"description"`
	PaymentPurpose  string         `json:"payment_purpose"`
	IdempotencyKey  string         `json:"idempotency_key"`
	OrderID         string         `json:"order_id"`
	QRType          string         `json:"qr_type"`
	NotificationURL string         `json:"notification_url"`
	Metadata        map[string]any `json:"metadata"`
	Extra           map[string]any `json:"extra"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg})
}
