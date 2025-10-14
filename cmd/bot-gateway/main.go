package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	chi "github.com/go-chi/chi/v5"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/prometheus/client_golang/prometheus"

	"tg-digest-bot/internal/adapters/billingclient"
	"tg-digest-bot/internal/adapters/bot"
	"tg-digest-bot/internal/adapters/mtproto"
	"tg-digest-bot/internal/adapters/repo"
	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/config"
	"tg-digest-bot/internal/infra/db"
	"tg-digest-bot/internal/infra/log"
	"tg-digest-bot/internal/infra/metrics"
	"tg-digest-bot/internal/infra/queue"
	"tg-digest-bot/internal/usecase/channels"
	"tg-digest-bot/internal/usecase/schedule"
)

func main() {
	cfg := config.Load()
	logger := log.NewLogger(cfg.AppEnv)

	metrics.MustRegister(prometheus.DefaultRegisterer)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	pool, err := db.Connect(cfg.PGDSN)
	if err != nil {
		logger.Fatal().Err(err).Msg("не удалось подключиться к БД")
	}
	defer pool.Close()

	repoAdapter := repo.NewPostgres(pool)
	var billingAdapter domain.Billing
	if cfg.Billing.BaseURL != "" {
		client, err := billingclient.New(cfg.Billing.BaseURL, billingclient.WithTimeout(cfg.Billing.Timeout))
		if err != nil {
			logger.Fatal().Err(err).Msg("бот: некорректная конфигурация биллинга")
		}
		billingAdapter = client
	} else {
		logger.Warn().Msg("бот: не настроен BILLING_BASE_URL, функции биллинга отключены")
	}
	var sbpClient domain.BillingSBP
	if b, ok := billingAdapter.(domain.BillingSBP); ok {
		sbpClient = b
	}
	if cfg.RabbitURL == "" {
		logger.Fatal().Msg("не указан адрес RabbitMQ (RABBITMQ_URL)")
	}
	digestQueue, err := queue.NewRabbitDigestQueue(cfg.RabbitURL, cfg.Queues.Digest)
	if err != nil {
		logger.Fatal().Err(err).Msg("не удалось инициализировать очередь RabbitMQ")
	}
	if cfg.MTProto.SessionName == "" {
		logger.Fatal().Msg("не указан пул MTProto-аккаунтов (MTPROTO_SESSION_NAME)")
	}
	accountCtx, accountCancel := context.WithTimeout(ctx, 10*time.Second)
	accountsMeta, err := repoAdapter.ListMTProtoAccounts(accountCtx, cfg.MTProto.SessionName)
	accountCancel()
	if err != nil {
		logger.Fatal().Err(err).Msg("не удалось загрузить MTProto-аккаунты")
	}
	if len(accountsMeta) == 0 {
		logger.Fatal().Msg("пул MTProto-аккаунтов пуст")
	}
	resolverAccounts := make([]mtproto.Account, 0, len(accountsMeta))
	for _, meta := range accountsMeta {
		resolverAccounts = append(resolverAccounts, mtproto.Account{
			Name:    meta.Name,
			APIID:   meta.APIID,
			APIHash: meta.APIHash,
			Storage: mtproto.NewSessionDB(repoAdapter, meta.Name),
		})
	}
	resolver, err := mtproto.NewResolver(resolverAccounts, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("не удалось создать MTProto резолвер")
	}
	channelService := channels.NewService(repoAdapter, resolver, repoAdapter)
	scheduleService := schedule.NewService(repoAdapter)

	botAPI, err := tgbotapi.NewBotAPI(cfg.Telegram.Token)
	if err != nil {
		logger.Fatal().Err(err).Msg("не удалось создать бота")
	}

	h := bot.NewHandler(botAPI, logger, channelService, scheduleService, repoAdapter, billingAdapter, sbpClient, digestQueue, cfg.Limits.DigestMax)

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

	metrics.StartServer(ctx, logger.With().Str("component", "metrics").Logger(), ":9090")
	go func() {
		logger.Info().Msg("бот-гейтвей запущен")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("HTTP сервер остановлен")
		}
	}()

	<-ctx.Done()
	logger.Info().Msg("остановка бота")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

var _ domain.UserRepo = (*repo.Postgres)(nil)
var _ domain.ChannelRepo = (*repo.Postgres)(nil)
var _ domain.PostRepo = (*repo.Postgres)(nil)
var _ domain.DigestRepo = (*repo.Postgres)(nil)
