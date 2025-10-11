package main

import (
	"context"
	"errors"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redis/go-redis/v9"
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(cfg.PGDSN)
	if err != nil {
		logger.Fatal().Err(err).Msg("collector: нет подключения к БД")
	}
	defer pool.Close()

	repoAdapter := repo.NewPostgres(pool)

	if cfg.RedisAddr == "" {
		logger.Fatal().Msg("collector: не указан адрес Redis (REDIS_ADDR)")
	}
	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer redisClient.Close()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Fatal().Err(err).Msg("collector: не удалось подключиться к Redis")
	}
	digestQueue := queue.NewRedisDigestQueue(redisClient, cfg.Queues.Digest)

	if cfg.Telegram.Token == "" {
		logger.Fatal().Msg("collector: не указан токен Telegram (TG_BOT_TOKEN)")
	}
	botAPI, err := tgbotapi.NewBotAPI(cfg.Telegram.Token)
	if err != nil {
		logger.Fatal().Err(err).Msg("collector: не удалось создать бота")
	}

	if cfg.MTProto.SessionFile == "" {
		logger.Fatal().Msg("collector: не указан путь к MTProto-сессии (MTPROTO_SESSION_FILE)")
	}
	collectorSession := mtproto.NewSessionFile(cfg.MTProto.SessionFile)
	collector, err := mtproto.NewCollector(cfg.Telegram.APIID, cfg.Telegram.APIHash, collectorSession, logger)
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
		log:      logger,
		queue:    digestQueue,
		digests:  repoAdapter,
		users:    repoAdapter,
		channels: repoAdapter,
		service:  digestService,
		bot:      botAPI,
	}

	logger.Info().Msg("collector: запуск обработки очереди")
	worker.Run(ctx)
	logger.Info().Msg("collector: остановлен")
}

type jobWorker struct {
	log      zerolog.Logger
	queue    domain.DigestQueue
	digests  domain.DigestRepo
	users    domain.UserRepo
	channels domain.ChannelRepo
	service  *digestusecase.Service
	bot      *tgbotapi.BotAPI
}

func (w *jobWorker) Run(ctx context.Context) {
	for {
		job, err := w.queue.Pop(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			w.log.Error().Err(err).Msg("collector: ошибка чтения очереди")
			time.Sleep(time.Second)
			continue
		}
		w.handleJob(ctx, job)
	}
}

func (w *jobWorker) handleJob(ctx context.Context, job domain.DigestJob) {
	jobLog := w.log.With().Int64("user", job.UserTGID).Str("cause", string(job.Cause)).Int64("channel", job.ChannelID).Logger()
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
		return
	}
	userChannels, err := w.channels.ListUserChannels(user.ID, 100, 0)
	if err != nil {
		jobLog.Error().Err(err).Msg("collector: не удалось получить каналы")
		w.sendPlain(job.ChatID, "Не удалось получить список каналов. Попробуйте позже.")
		return
	}
	if len(userChannels) == 0 {
		w.sendPlain(job.ChatID, "Сначала добавьте хотя бы один канал командой /add")
		return
	}
	var channels []domain.Channel
	if job.ChannelID > 0 {
		for _, uc := range userChannels {
			if uc.ChannelID == job.ChannelID {
				channels = []domain.Channel{uc.Channel}
				break
			}
		}
		if len(channels) == 0 {
			w.sendPlain(job.ChatID, "Канал не найден среди ваших подписок")
			return
		}
	} else {
		channels = make([]domain.Channel, 0, len(userChannels))
		for _, uc := range userChannels {
			channels = append(channels, uc.Channel)
		}
	}
	if err := w.service.CollectNow(ctx, channels); err != nil {
		jobLog.Error().Err(err).Msg("collector: ошибка сбора постов")
		w.sendPlain(job.ChatID, "Не удалось собрать дайджест, попробуйте позже.")
		return
	}
	var (
		digest domain.Digest
		//err    error
	)
	if job.ChannelID > 0 {
		digest, err = w.service.BuildChannelForDate(job.UserTGID, job.ChannelID, job.Date)
	} else {
		digest, err = w.service.BuildForDate(job.UserTGID, job.Date)
	}
	if err != nil {
		if errors.Is(err, digestusecase.ErrChannelNotFound) {
			w.sendPlain(job.ChatID, "Канал недоступен для дайджеста")
			return
		}
		if errors.Is(err, digestusecase.ErrNoChannels) {
			w.sendPlain(job.ChatID, "Сначала добавьте хотя бы один канал командой /add")
			return
		}
		jobLog.Error().Err(err).Msg("collector: ошибка построения дайджеста")
		w.sendPlain(job.ChatID, "Не удалось построить дайджест, попробуйте позже.")
		return
	}
	if len(digest.Items) == 0 {
		if job.ChannelID > 0 {
			w.sendPlain(job.ChatID, "В выбранном канале за последние 24 часа ничего не найдено")
		} else {
			w.sendPlain(job.ChatID, "За последние 24 часа ничего не найдено")
		}
		return
	}
	if job.ChannelID == 0 {
		if err := w.persistDigest(digest); err != nil {
			jobLog.Error().Err(err).Msg("collector: не удалось сохранить дайджест")
		}
	}
	message := digestusecase.FormatDigest(digest)
	if err := w.sendDigest(job.ChatID, message); err != nil {
		if job.Cause == domain.DigestCauseManual {
			w.sendPlain(job.ChatID, "Не удалось собрать дайджест, попробуйте позже.")
		}
		jobLog.Error().Err(err).Msg("collector: отправка дайджеста")

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
