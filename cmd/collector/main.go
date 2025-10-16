package main

import (
	"context"
	"errors"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"tg-digest-bot/internal/adapters/mtproto"
	"tg-digest-bot/internal/adapters/ranker"
	"tg-digest-bot/internal/adapters/repo"
	"tg-digest-bot/internal/adapters/summarizer"
	"tg-digest-bot/internal/adapters/telegram"
	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/config"
	"tg-digest-bot/internal/infra/db"
	applog "tg-digest-bot/internal/infra/log"
	"tg-digest-bot/internal/infra/metrics"
	"tg-digest-bot/internal/infra/openai"
	"tg-digest-bot/internal/infra/queue"
	digestusecase "tg-digest-bot/internal/usecase/digest"
)

func main() {
	cfg := config.Load()
	logger := applog.NewLogger(cfg.AppEnv)

	metrics.MustRegister(prometheus.DefaultRegisterer)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	metrics.StartServer(ctx, logger.With().Str("component", "metrics").Logger(), ":9090")

	pool, err := db.Connect(cfg.PGDSN)
	if err != nil {
		logger.Fatal().Err(err).Msg("collector: нет подключения к БД")
	}
	defer pool.Close()

	repoAdapter := repo.NewPostgres(pool)

	if cfg.RabbitURL == "" {
		logger.Fatal().Msg("collector: не указан адрес RabbitMQ (RABBITMQ_URL)")
	}
	digestQueue, err := queue.NewRabbitDigestQueue(cfg.RabbitURL, cfg.Queues.Digest)
	if err != nil {
		logger.Fatal().Err(err).Msg("collector: не удалось инициализировать очередь RabbitMQ")
	}

	if cfg.Telegram.Token == "" {
		logger.Fatal().Msg("collector: не указан токен Telegram (TG_BOT_TOKEN)")
	}
	botAPI, err := tgbotapi.NewBotAPI(cfg.Telegram.Token)
	if err != nil {
		logger.Fatal().Err(err).Msg("collector: не удалось создать бота")
	}

	if cfg.MTProto.SessionName == "" {
		logger.Fatal().Msg("collector: не указан пул MTProto-аккаунтов (MTPROTO_SESSION_NAME)")
	}
	accountCtx, accountCancel := context.WithTimeout(ctx, 10*time.Second)
	accountsMeta, err := repoAdapter.ListMTProtoAccounts(accountCtx, cfg.MTProto.SessionName)
	accountCancel()
	if err != nil {
		logger.Fatal().Err(err).Msg("collector: не удалось загрузить MTProto-аккаунты")
	}
	if len(accountsMeta) == 0 {
		logger.Fatal().Msg("collector: пул MTProto-аккаунтов пуст")
	}
	collectorAccounts := make([]mtproto.Account, 0, len(accountsMeta))
	for _, meta := range accountsMeta {
		collectorAccounts = append(collectorAccounts, mtproto.Account{
			Name:    meta.Name,
			APIID:   meta.APIID,
			APIHash: meta.APIHash,
			Storage: mtproto.NewSessionDB(repoAdapter, meta.Name),
		})
	}
	collector, err := mtproto.NewCollector(collectorAccounts, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("collector: не удалось создать MTProto клиента")
	}

	if cfg.OpenAI.APIKey == "" {
		logger.Fatal().Msg("collector: не указан ключ OpenAI (OPENAI_API_KEY)")
	}
	openaiClient := openai.NewClient(cfg.OpenAI.APIKey, cfg.OpenAI.BaseURL, cfg.OpenAI.Timeout)

	summarizerAdapter := summarizer.NewOpenAI(openaiClient, cfg.OpenAI.Model, cfg.OpenAI.Timeout)
	rankerAdapter := ranker.NewLLM(openaiClient, cfg.OpenAI.Model, cfg.OpenAI.Timeout, cfg.Limits.DigestMax)
	digestService := digestusecase.NewService(repoAdapter, repoAdapter, repoAdapter, repoAdapter, summarizerAdapter, rankerAdapter, collector, cfg.Limits.DigestMax)

	worker := &jobWorker{
		log:       logger,
		queue:     digestQueue,
		digests:   repoAdapter,
		users:     repoAdapter,
		channels:  repoAdapter,
		statuses:  repoAdapter,
		analytics: repoAdapter,
		service:   digestService,
		bot:       botAPI,
	}

	logger.Info().Msg("collector: запуск обработки очереди")
	worker.Run(ctx)
	logger.Info().Msg("collector: остановлен")
}

type jobWorker struct {
	log       zerolog.Logger
	queue     domain.DigestQueue
	digests   domain.DigestRepo
	users     domain.UserRepo
	channels  domain.ChannelRepo
	statuses  domain.DigestJobStatusRepo
	analytics domain.BusinessMetricRepo
	service   *digestusecase.Service
	bot       *tgbotapi.BotAPI
}

const maxDeliveryAttempts = 5

type jobOutcome int

const (
	jobOutcomeCompleted jobOutcome = iota
	jobOutcomeRetry
)

func (w *jobWorker) Run(ctx context.Context) {
	for {
		job, ack, err := w.queue.Receive(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			w.log.Error().Err(err).Msg("collector: ошибка чтения очереди")
			time.Sleep(time.Second)
			continue
		}

		jobLog := w.log.With().
			Str("job_id", job.ID).
			Int64("user", job.UserTGID).
			Str("cause", string(job.Cause)).
			Int64("channel", job.ChannelID).
			Strs("tags", job.Tags).
			Logger()

		if job.ID == "" {
			jobLog.Error().Msg("collector: получена задача без идентификатора, подтверждаем и пропускаем")
			if err := ack(true); err != nil {
				jobLog.Error().Err(err).Msg("collector: не удалось подтвердить задачу без идентификатора")
			}
			continue
		}

		delivered, attempt, err := w.statuses.EnsureDigestJob(job.ID)
		if err != nil {
			jobLog.Error().Err(err).Msg("collector: не удалось зарегистрировать задачу")
			if ackErr := ack(false); ackErr != nil {
				jobLog.Error().Err(ackErr).Msg("collector: не удалось вернуть задачу в очередь")
			}
			time.Sleep(time.Second)
			continue
		}

		jobLog = jobLog.With().Int("attempt", attempt).Logger()

		if delivered {
			jobLog.Info().Msg("collector: задача уже была доставлена, подтверждаем")
			if err := ack(true); err != nil {
				jobLog.Error().Err(err).Msg("collector: не удалось подтвердить ранее доставленную задачу")
			}
			continue
		}

		outcome := w.handleJob(ctx, job, attempt, jobLog)

		if outcome == jobOutcomeRetry && attempt < maxDeliveryAttempts {
			jobLog.Warn().Msg("collector: задача завершилась ошибкой, повторим позже")
			if err := ack(false); err != nil {
				jobLog.Error().Err(err).Msg("collector: не удалось вернуть задачу после ошибки")
			}
			continue
		}

		if outcome == jobOutcomeRetry {
			jobLog.Error().Msg("collector: достигнут предел попыток, помечаем задачу как завершённую")
		}

		if err := w.statuses.MarkDigestJobDelivered(job.ID); err != nil {
			jobLog.Error().Err(err).Msg("collector: не удалось пометить задачу доставленной")
			if ackErr := ack(false); ackErr != nil {
				jobLog.Error().Err(ackErr).Msg("collector: не удалось вернуть задачу после ошибки статуса")
			}
			time.Sleep(time.Second)
			continue
		}

		if err := ack(true); err != nil {
			jobLog.Error().Err(err).Msg("collector: не удалось подтвердить задачу")
		}
	}
}

func (w *jobWorker) handleJob(ctx context.Context, job domain.DigestJob, attempt int, jobLog zerolog.Logger) jobOutcome {
	if job.ChatID == 0 {
		job.ChatID = job.UserTGID
	}
	if job.Date.IsZero() {
		job.Date = time.Now().UTC()
	}
	user, err := w.users.GetByTGID(job.UserTGID)
	if err != nil {
		jobLog.Error().Err(err).Msg("collector: пользователь не найден")
		w.sendPlain(job.ChatID, "Не удалось найти ваш профиль. Отправьте /start в боте и попробуйте снова.")
		return jobOutcomeCompleted
	}
	userChannels, err := w.channels.ListUserChannels(user.ID, 100, 0)
	if err != nil {
		jobLog.Error().Err(err).Msg("collector: не удалось получить каналы")
		w.sendPlain(job.ChatID, "Не удалось получить список каналов. Попробуйте позже.")
		return jobOutcomeCompleted
	}
	if len(userChannels) == 0 {
		w.sendPlain(job.ChatID, "Сначала добавьте хотя бы один канал командой /add")
		return jobOutcomeCompleted
	}
	var channels []domain.Channel
	switch {
	case job.ChannelID > 0:
		for _, uc := range userChannels {
			if uc.ChannelID == job.ChannelID {
				channels = []domain.Channel{uc.Channel}
				break
			}
		}
		if len(channels) == 0 {
			w.sendPlain(job.ChatID, "Канал не найден среди ваших подписок")
			return jobOutcomeCompleted
		}
	case len(job.Tags) > 0:
		matched := make([]domain.Channel, 0)
		for _, uc := range userChannels {
			if matchesAnyTag(uc.Tags, job.Tags) {
				matched = append(matched, uc.Channel)
			}
		}
		if len(matched) == 0 {
			w.sendPlain(job.ChatID, "Не найдено каналов с такими тегами")
			return jobOutcomeCompleted
		}
		channels = matched
	default:
		channels = make([]domain.Channel, 0, len(userChannels))
		for _, uc := range userChannels {
			channels = append(channels, uc.Channel)
		}
	}
	if err := w.service.CollectNow(ctx, channels); err != nil {
		jobLog.Error().Err(err).Msg("collector: ошибка сбора постов")
		w.sendPlain(job.ChatID, "Не удалось собрать дайджест, попробуйте позже.")
		return jobOutcomeCompleted
	}
	var (
		digest domain.Digest
		//err    error
	)
	switch {
	case job.ChannelID > 0:
		digest, err = w.service.BuildChannelForDate(job.UserTGID, job.ChannelID, job.Date)
	case len(job.Tags) > 0:
		digest, err = w.service.BuildTagsForDate(job.UserTGID, job.Tags, job.Date)
	default:
		digest, err = w.service.BuildForDate(job.UserTGID, job.Date)
	}
	if err != nil {
		if errors.Is(err, digestusecase.ErrChannelNotFound) {
			w.sendPlain(job.ChatID, "Канал недоступен для дайджеста")
			return jobOutcomeCompleted
		}
		if errors.Is(err, digestusecase.ErrNoChannels) {
			w.sendPlain(job.ChatID, "Сначала добавьте хотя бы один канал командой /add")
			return jobOutcomeCompleted
		}
		jobLog.Error().Err(err).Msg("collector: ошибка построения дайджеста")
		w.sendPlain(job.ChatID, "Не удалось построить дайджест, попробуйте позже.")
		return jobOutcomeCompleted
	}
	if len(digest.Items) == 0 {
		switch {
		case job.ChannelID > 0:
			w.sendPlain(job.ChatID, "В выбранном канале за последние 24 часа ничего не найдено")
		case len(job.Tags) > 0:
			w.sendPlain(job.ChatID, "Не удалось найти новые посты по выбранным тегам")
		default:
			w.sendPlain(job.ChatID, "За последние 24 часа ничего не найдено")
		}
		return jobOutcomeCompleted
	}
	if job.ChannelID == 0 && len(job.Tags) == 0 {
		if err := w.persistDigest(digest); err != nil {
			jobLog.Error().Err(err).Msg("collector: не удалось сохранить дайджест")
		}
	}
	message := digestusecase.FormatDigest(digest)
	if err := w.sendDigest(job.ChatID, message); err != nil {
		if job.Cause == domain.DigestCauseManual && attempt == 1 {
			w.sendPlain(job.ChatID, "Не удалось собрать дайджест, попробуйте позже.")
		}
		jobLog.Error().Err(err).Msg("collector: отправка дайджеста")
		return jobOutcomeRetry
	}
	w.observeDigestDelivery(ctx, job, user, digest, attempt)
	return jobOutcomeCompleted
}

func (w *jobWorker) observeDigestDelivery(ctx context.Context, job domain.DigestJob, user domain.User, digest domain.Digest, attempt int) {
	if w.analytics == nil {
		return
	}
	userID := user.ID
	meta := map[string]any{
		"job_id":       job.ID,
		"cause":        string(job.Cause),
		"attempt":      attempt,
		"items_count":  len(digest.Items),
		"requested_at": job.RequestedAt,
		"date":         job.Date,
		"delivered_at": time.Now().UTC(),
		"chat_id":      job.ChatID,
	}
	metric := domain.BusinessMetric{
		Event:    domain.BusinessMetricEventDigestDelivered,
		UserID:   &userID,
		Metadata: meta,
	}
	if job.ChannelID > 0 {
		channelID := job.ChannelID
		meta["channel_id"] = job.ChannelID
		metric.ChannelID = &channelID
	}
	if len(job.Tags) > 0 {
		meta["tags"] = job.Tags
	}
	if err := w.analytics.RecordBusinessMetric(ctx, metric); err != nil {
		w.log.Error().Err(err).Str("event", domain.BusinessMetricEventDigestDelivered).Msg("collector: не удалось сохранить бизнес-метрику")
	}
}

func (w *jobWorker) persistDigest(d domain.Digest) error {
	saved, err := w.digests.CreateDigest(d)
	if err != nil {
		return err
	}
	return w.digests.MarkDelivered(saved.UserID, saved.Date)
}

func (w *jobWorker) sendPlain(chatID int64, text string) {
	parts := telegram.SplitMessage(text)
	for _, part := range parts {
		msg := tgbotapi.NewMessage(chatID, part)
		start := time.Now()
		_, err := w.bot.Send(msg)
		metrics.ObserveNetworkRequest("telegram_bot", "send_message", strconv.FormatInt(chatID, 10), start, err)
		if err != nil {
			w.log.Error().Err(err).Int64("chat", chatID).Msg("collector: не удалось отправить сообщение")
			return
		}
	}
}

func (w *jobWorker) sendDigest(chatID int64, text string) error {
	parts := telegram.SplitMessage(text)
	for _, part := range parts {
		msg := tgbotapi.NewMessage(chatID, part)
		msg.ParseMode = tgbotapi.ModeHTML
		msg.DisableWebPagePreview = true
		start := time.Now()
		_, err := w.bot.Send(msg)
		metrics.ObserveNetworkRequest("telegram_bot", "send_message", strconv.FormatInt(chatID, 10), start, err)
		if err != nil {
			return err
		}
	}
	return nil
}

func matchesAnyTag(channelTags, requested []string) bool {
	if len(channelTags) == 0 || len(requested) == 0 {
		return false
	}
	lookup := make(map[string]struct{}, len(channelTags))
	for _, tag := range channelTags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			continue
		}
		lookup[strings.ToLower(trimmed)] = struct{}{}
	}
	for _, tag := range requested {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			continue
		}
		if _, ok := lookup[strings.ToLower(trimmed)]; ok {
			return true
		}
	}
	return false
}
