package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	chi "github.com/go-chi/chi/v5"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"tg-digest-bot/internal/adapters/bot"
	"tg-digest-bot/internal/adapters/mtproto"
	"tg-digest-bot/internal/adapters/ranker"
	"tg-digest-bot/internal/adapters/repo"
	"tg-digest-bot/internal/adapters/summarizer"
	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/config"
	"tg-digest-bot/internal/infra/db"
	"tg-digest-bot/internal/infra/log"
	"tg-digest-bot/internal/usecase/channels"
	"tg-digest-bot/internal/usecase/digest"
	"tg-digest-bot/internal/usecase/schedule"
)

func main() {
	cfg := config.Load()
	logger := log.NewLogger(cfg.AppEnv)

	pool, err := db.Connect(cfg.PGDSN)
	if err != nil {
		logger.Fatal().Err(err).Msg("не удалось подключиться к БД")
	}
	defer pool.Close()

	repoAdapter := repo.NewPostgres(pool)
	summarizerAdapter := summarizer.NewSimple()
	rankerAdapter := ranker.NewSimple(24)
	collectorSession := &mtproto.SessionInMemory{}
	collector, _ := mtproto.NewCollector(cfg.Telegram.APIID, cfg.Telegram.APIHash, collectorSession, logger)
	digestService := digest.NewService(repoAdapter, repoAdapter, repoAdapter, repoAdapter, summarizerAdapter, rankerAdapter, collector, cfg.Limits.DigestMax)
	channelService := channels.NewService(repoAdapter, mtproto.NewResolver(logger), repoAdapter, cfg.Limits.FreeChannels)
	scheduleService := schedule.NewService(repoAdapter)

	botAPI, err := tgbotapi.NewBotAPI(cfg.Telegram.Token)
	if err != nil {
		logger.Fatal().Err(err).Msg("не удалось создать бота")
	}

	h := bot.NewHandler(botAPI, logger, channelService, digestService, scheduleService, repoAdapter, repoAdapter, cfg.Limits.FreeChannels, cfg.Limits.DigestMax)

	r := chi.NewRouter()
	r.Post("/bot/webhook", func(w http.ResponseWriter, r *http.Request) {
		var update tgbotapi.Update
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.HandleUpdate(r.Context(), update)
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Addr: ":8080", Handler: r}
	go func() {
		logger.Info().Msg("бот-гейтвей запущен")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("HTTP сервер остановлен")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
	logger.Info().Msg("остановка бота")
	ctx, cancel := context.WithTimeout(context.Background(), 5*1e9)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

var _ domain.UserRepo = (*repo.Postgres)(nil)
var _ domain.ChannelRepo = (*repo.Postgres)(nil)
var _ domain.PostRepo = (*repo.Postgres)(nil)
var _ domain.DigestRepo = (*repo.Postgres)(nil)
