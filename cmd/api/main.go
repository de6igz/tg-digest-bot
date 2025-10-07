package main

import (
	"encoding/json"
	"net/http"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"tg-digest-bot/internal/adapters/repo"
	"tg-digest-bot/internal/infra/config"
	"tg-digest-bot/internal/infra/db"
	httpinfra "tg-digest-bot/internal/infra/http"
)

func main() {
	cfg := config.Load()
	pool, err := db.Connect(cfg.PGDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("api: нет подключения к БД")
	}
	defer pool.Close()

	repoAdapter := repo.NewPostgres(pool)

	r := chi.NewRouter()
	r.Use(httpinfra.WebAppAuthMiddleware(cfg.Telegram.Token))

	r.Get("/api/v1/digest/today", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"date":  time.Now().Format("2006-01-02"),
			"items": []map[string]string{},
		}
		writeJSON(w, resp)
	})

	r.Get("/api/v1/digest/history", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"history": []any{}}
		writeJSON(w, resp)
	})

	r.Get("/api/v1/channels", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{})
	})

	r.Post("/api/v1/channels", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})

	r.Delete("/api/v1/channels/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	r.Put("/api/v1/settings/time", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})

	srv := &http.Server{Addr: ":8080", Handler: r}
	log.Info().Msg("api: старт")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal().Err(err).Msg("api: сервер остановлен")
	}

	_ = repoAdapter
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
