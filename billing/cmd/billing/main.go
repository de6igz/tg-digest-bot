package main

import (
	"context"
	"crypto/rsa"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"billing/internal/config"
	httpapi "billing/internal/http"
	"billing/internal/storage"
	"billing/internal/tochka"
	sbpusecase "billing/internal/usecase/sbp"
)

func main() {
	cfg := config.Load()
	setupLogger()

	if cfg.PGDSN == "" {
		log.Fatal().Msg("billing: BILLING_PG_DSN is required")
	}
	if cfg.APIToken == "" {
		log.Fatal().Msg("billing: BILLING_API_TOKEN is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := connectDB(ctx, cfg.PGDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("billing: failed to connect to postgres")
	}
	defer pool.Close()

	billingRepo := storage.NewPostgres(pool)

	var sbpService *sbpusecase.Service
	var webhookKey *rsa.PublicKey
	if cfg.Tochka.MerchantID != "" && cfg.Tochka.AccountID != "" && cfg.Tochka.AccessToken != "" {
		tochkaClient := tochka.NewClient(tochka.Config{
			BaseURL:     cfg.Tochka.BaseURL,
			MerchantID:  cfg.Tochka.MerchantID,
			AccountID:   cfg.Tochka.AccountID,
			AccessToken: cfg.Tochka.AccessToken,
			Timeout:     cfg.Tochka.Timeout,
		})
		sbpService = sbpusecase.NewService(billingRepo, tochkaClient, cfg.Tochka.NotificationURL, log.With().Str("component", "sbp").Logger())
		if cfg.Tochka.NotificationURL == "" {
			log.Warn().Msg("billing: TOCHKA_NOTIFICATION_URL is not set, webhook callbacks may fail")
		}
		if cfg.Tochka.WebhookKey != "" {
			key, err := tochka.ParseRSAPublicKeyFromJWK([]byte(cfg.Tochka.WebhookKey))
			if err != nil {
				log.Fatal().Err(err).Msg("billing: invalid tochka webhook key")
			}
			webhookKey = key
		}
	} else {
		log.Warn().Msg("billing: tochka credentials are not fully configured, SBP endpoints disabled")
	}

	opts := []httpapi.Option{httpapi.WithLogger(log.Logger)}
	if sbpService != nil {
		opts = append(opts, httpapi.WithSBPService(sbpService, cfg.Tochka.WebhookSecret, webhookKey))
	}
	opts = append(opts, httpapi.WithAuthToken(cfg.APIToken))

	server := httpapi.NewServer(billingRepo, opts...)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      server.Router(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	go func() {
		log.Info().Int("port", cfg.Port).Msg("billing: server started")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("billing: server stopped")
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("billing: graceful shutdown failed")
	}
}

func setupLogger() {
	output := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	log.Logger = zerolog.New(output).With().Timestamp().Logger()
}

func connectDB(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return pool, nil
}
