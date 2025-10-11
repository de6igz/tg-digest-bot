package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog"

	"tg-digest-bot/internal/adapters/telegram"
	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/usecase/channels"
	"tg-digest-bot/internal/usecase/schedule"
)

// Handler обслуживает вебхук бота.
type Handler struct {
	bot         *tgbotapi.BotAPI
	log         zerolog.Logger
	channelUC   *channels.Service
	scheduleUC  *schedule.Service
	users       domain.UserRepo
	jobs        domain.DigestQueue
	freeLimit   int
	maxDigest   int
	mu          sync.Mutex
	pendingDrop map[int64]time.Time
}

// NewHandler создаёт обработчик.
func NewHandler(bot *tgbotapi.BotAPI, log zerolog.Logger, channelUC *channels.Service, scheduleUC *schedule.Service, userRepo domain.UserRepo, jobs domain.DigestQueue, freeLimit, maxDigest int) *Handler {
	return &Handler{
		bot:         bot,
		log:         log,
		channelUC:   channelUC,
		scheduleUC:  scheduleUC,
		users:       userRepo,
		jobs:        jobs,
		freeLimit:   freeLimit,
		maxDigest:   maxDigest,
		pendingDrop: make(map[int64]time.Time),
	}
}

// HandleUpdate обрабатывает входящий апдейт.
func (h *Handler) HandleUpdate(ctx context.Context, upd tgbotapi.Update) {
	if upd.Message != nil {
		h.handleMessage(ctx, upd.Message)
	} else if upd.CallbackQuery != nil {
		h.handleCallback(ctx, upd.CallbackQuery)
	}
}

func (h *Handler) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	text := strings.TrimSpace(msg.Text)
	switch {
	case strings.HasPrefix(text, "/start"):
		h.handleStart(ctx, msg)
	case strings.HasPrefix(text, "/help"):
		h.handleHelp(msg.Chat.ID)
	case strings.HasPrefix(text, "/add"):
		alias := strings.TrimSpace(strings.TrimPrefix(text, "/add"))
		h.handleAdd(ctx, msg.Chat.ID, msg.From.ID, alias)
	case strings.HasPrefix(text, "/list"):
		h.handleList(ctx, msg.Chat.ID, msg.From.ID)
	case strings.HasPrefix(text, "/digest_now"):
		h.handleDigestNow(ctx, msg.Chat.ID, msg.From.ID)
	case strings.HasPrefix(text, "/schedule"):
		h.handleSchedule(msg.Chat.ID)
	case strings.HasPrefix(text, "/mute"):
		alias := strings.TrimSpace(strings.TrimPrefix(text, "/mute"))
		h.handleMuteCommand(ctx, msg.Chat.ID, msg.From.ID, alias, true)
	case strings.HasPrefix(text, "/unmute"):
		alias := strings.TrimSpace(strings.TrimPrefix(text, "/unmute"))
		h.handleMuteCommand(ctx, msg.Chat.ID, msg.From.ID, alias, false)
	case strings.HasPrefix(text, "/clear_data"):
		h.handleClearRequest(msg.Chat.ID, msg.From.ID)
	case strings.HasPrefix(text, "/clear_data_confirm"):
		h.handleClearConfirm(ctx, msg.Chat.ID, msg.From.ID)
	default:
		h.reply(msg.Chat.ID, "Неизвестная команда. Используйте /help", nil)
	}
}

func (h *Handler) handleStart(ctx context.Context, msg *tgbotapi.Message) {
	if msg.From == nil {
		h.reply(msg.Chat.ID, "Не удалось определить пользователя", nil)
		return
	}
	locale := msg.From.LanguageCode
	if _, err := h.users.UpsertByTGID(msg.From.ID, locale, ""); err != nil {
		h.reply(msg.Chat.ID, fmt.Sprintf("Ошибка сохранения профиля: %v", err), nil)
		return
	}
	welcome := "👋 Добро пожаловать! Управляйте каналами и получайте дайджесты." +
		fmt.Sprintf("\nЛимит каналов: %d. Используйте кнопки ниже.", h.freeLimit)
	h.reply(msg.Chat.ID, welcome, h.mainKeyboard())
}

func (h *Handler) handleHelp(chatID int64) {
	help := strings.Join([]string{
		"Команды:",
		"/start — регистрация",
		"/add @alias — добавить канал",
		"/list — показать каналы",
		"/digest_now — получить дайджест",
		"/schedule — настроить время",
		"/mute @alias — выключить уведомления",
		"/unmute @alias — включить уведомления",
		"/clear_data — удалить данные",
	}, "\n")
	h.reply(chatID, help, nil)
}

func (h *Handler) handleAdd(ctx context.Context, chatID int64, tgUserID int64, alias string) {
	if alias == "" {
		h.reply(chatID, "Отправьте /add @alias", nil)
		return
	}
	channel, err := h.channelUC.AddChannel(ctx, tgUserID, alias)
	if err != nil {
		switch {
		case errors.Is(err, channels.ErrAliasInvalid):
			h.reply(chatID, "Некорректный алиас. Пример: /add @example", nil)
		case errors.Is(err, channels.ErrChannelLimit):
			h.reply(chatID, fmt.Sprintf("Превышен лимит %d каналов. Удалите канал перед добавлением нового.", h.freeLimit), nil)
		case errors.Is(err, channels.ErrPrivateChannel):
			h.reply(chatID, "Канал приватный или недоступен. Добавьте публичный канал.", nil)
		default:
			h.reply(chatID, fmt.Sprintf("Ошибка добавления: %v", err), nil)
		}
		return
	}
	title := channel.Title
	if title == "" {
		title = channel.Alias
	}
	h.reply(chatID, fmt.Sprintf("Готово: %s", title), h.mainKeyboard())
}

func (h *Handler) handleList(ctx context.Context, chatID int64, tgUserID int64) {
	channels, err := h.channelUC.ListChannels(ctx, tgUserID, 10, 0)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Ошибка: %v", err), nil)
		return
	}
	if len(channels) == 0 {
		h.reply(chatID, "У вас пока нет каналов", nil)
		return
	}
	var b strings.Builder
	for i, ch := range channels {
		title := ch.Channel.Title
		if title == "" {
			title = ch.Channel.Alias
		}
		fmt.Fprintf(&b, "%d. %s (@%s)\n", i+1, title, ch.Channel.Alias)
	}
	keyboard := make([][]tgbotapi.InlineKeyboardButton, 0, len(channels))
	for _, ch := range channels {
		action := "mute"
		label := "🔕 Мьют"
		if ch.Muted {
			action = "unmute"
			label = "🔔 Вкл"
		}
		toggle := tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("%s:%d", action, ch.ChannelID))
		del := tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить", fmt.Sprintf("delete:%d", ch.ChannelID))
		keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(toggle, del))
	}
	markup := tgbotapi.NewInlineKeyboardMarkup(keyboard...)
	h.reply(chatID, b.String(), &markup)
}

func (h *Handler) handleDigestNow(ctx context.Context, chatID int64, tgUserID int64) {
	channels, err := h.channelUC.ListChannels(ctx, tgUserID, 100, 0)
	if err != nil {
		h.log.Error().Err(err).Int64("user", tgUserID).Msg("не удалось получить каналы пользователя")
		h.reply(chatID, "Не удалось получить список каналов. Попробуйте позже", nil)
		return
	}
	if len(channels) == 0 {
		h.reply(chatID, "Сначала добавьте хотя бы один канал командой /add", nil)
		return
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(channels)+1)
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("📰 Все каналы", "digest_all"),
	))
	for _, ch := range channels {
		title := ch.Channel.Title
		if title == "" {
			title = ch.Channel.Alias
		}
		button := tgbotapi.NewInlineKeyboardButtonData(title, fmt.Sprintf("digest_channel:%d", ch.ChannelID))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(button))
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	h.reply(chatID, "Выберите дайджест за последние 24 часа", &markup)
}

func (h *Handler) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	data := cb.Data
	switch {
	case data == "add_channel":
		h.reply(cb.Message.Chat.ID, "Отправьте /add @alias", nil)
	case data == "digest_now":
		h.handleDigestNow(ctx, cb.Message.Chat.ID, cb.From.ID)
	case data == "digest_all":
		h.enqueueDigest(ctx, cb.Message.Chat.ID, cb.From.ID, 0)
	case strings.HasPrefix(data, "digest_channel:"):
		id := parseID(data)
		h.enqueueDigest(ctx, cb.Message.Chat.ID, cb.From.ID, id)
	case data == "my_channels":
		h.handleList(ctx, cb.Message.Chat.ID, cb.From.ID)
	case data == "set_time":
		h.handleSchedule(cb.Message.Chat.ID)
	case strings.HasPrefix(data, "set_time:"):
		value := strings.TrimPrefix(data, "set_time:")
		h.handleSetTime(ctx, cb.Message.Chat.ID, cb.From.ID, value)
	case strings.HasPrefix(data, "mute:"):
		id := parseID(data)
		h.toggleMute(ctx, cb.Message.Chat.ID, cb.From.ID, id, true)
	case strings.HasPrefix(data, "unmute:"):
		id := parseID(data)
		h.toggleMute(ctx, cb.Message.Chat.ID, cb.From.ID, id, false)
	case strings.HasPrefix(data, "delete:"):
		id := parseID(data)
		h.handleDeleteChannel(ctx, cb.Message.Chat.ID, cb.From.ID, id)
	case data == "more_items":
		h.reply(cb.Message.Chat.ID, fmt.Sprintf("Пока доступно только %d элементов. Обновите дайджест позже.", h.maxDigest), nil)
	}
	_, _ = h.bot.Request(tgbotapi.NewCallback(cb.ID, ""))
}

func (h *Handler) handleSchedule(chatID int64) {
	h.reply(chatID, "Выберите время ежедневной доставки", SchedulePresetKeyboard())
}

func (h *Handler) handleSetTime(ctx context.Context, chatID, tgUserID int64, value string) {
	tm, err := ParseLocalTime(value)
	if err != nil {
		h.reply(chatID, "Некорректный формат времени. Используйте ЧЧ:ММ", nil)
		return
	}
	if err := h.scheduleUC.UpdateDailyTime(ctx, tgUserID, tm); err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось сохранить время: %v", err), nil)
		return
	}
	h.reply(chatID, fmt.Sprintf("Время доставки установлено на %s", value), nil)
}

func (h *Handler) handleMuteCommand(ctx context.Context, chatID, tgUserID int64, alias string, mute bool) {
	if alias == "" {
		h.reply(chatID, "Укажите алиас канала, например /mute @example", nil)
		return
	}
	parsed, err := channels.ParseAlias(alias)
	if err != nil {
		h.reply(chatID, "Некорректный алиас", nil)
		return
	}
	channelsList, err := h.channelUC.ListChannels(ctx, tgUserID, 100, 0)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Ошибка получения каналов: %v", err), nil)
		return
	}
	var channelID int64
	for _, ch := range channelsList {
		if strings.EqualFold(ch.Channel.Alias, parsed) {
			channelID = ch.ChannelID
			break
		}
	}
	if channelID == 0 {
		h.reply(chatID, "Канал не найден среди ваших подписок", nil)
		return
	}
	h.toggleMute(ctx, chatID, tgUserID, channelID, mute)
}

func (h *Handler) toggleMute(ctx context.Context, chatID, tgUserID, channelID int64, mute bool) {
	if channelID == 0 {
		h.reply(chatID, "Некорректный идентификатор канала", nil)
		return
	}
	if err := h.channelUC.ToggleMute(ctx, tgUserID, channelID, mute); err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось обновить статус: %v", err), nil)
		return
	}
	if mute {
		h.reply(chatID, "Канал выключен в дайджесте", nil)
	} else {
		h.reply(chatID, "Канал снова участвует в дайджесте", nil)
	}
}

func (h *Handler) handleDeleteChannel(ctx context.Context, chatID, tgUserID, channelID int64) {
	if channelID == 0 {
		h.reply(chatID, "Некорректный идентификатор", nil)
		return
	}
	if err := h.channelUC.RemoveChannel(ctx, tgUserID, channelID); err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось удалить: %v", err), nil)
		return
	}
	h.reply(chatID, "Канал удалён", nil)
}

func (h *Handler) enqueueDigest(ctx context.Context, chatID, tgUserID, channelID int64) {
	job := domain.DigestJob{
		UserTGID:    tgUserID,
		ChatID:      chatID,
		ChannelID:   channelID,
		Date:        time.Now().UTC(),
		RequestedAt: time.Now().UTC(),
		Cause:       domain.DigestCauseManual,
	}

	var channelName string
	if channelID > 0 {
		channels, err := h.channelUC.ListChannels(ctx, tgUserID, 100, 0)
		if err != nil {
			h.log.Error().Err(err).Int64("user", tgUserID).Msg("не удалось получить каналы пользователя")
			h.reply(chatID, "Не удалось получить список каналов. Попробуйте позже", nil)
			return
		}
		for _, ch := range channels {
			if ch.ChannelID == channelID {
				channelName = ch.Channel.Title
				if channelName == "" {
					channelName = ch.Channel.Alias
				}
				break
			}
		}
		if channelName == "" {
			h.reply(chatID, "Канал не найден среди ваших подписок", nil)
			return
		}
	}

	if err := h.jobs.Enqueue(ctx, job); err != nil {
		h.log.Error().Err(err).Int64("user", tgUserID).Int64("channel", channelID).Msg("не удалось поставить задачу дайджеста")
		h.reply(chatID, "Не удалось поставить дайджест в очередь, попробуйте позже", nil)
		return
	}

	if channelID > 0 {
		h.reply(chatID, fmt.Sprintf("Собираем дайджест по каналу %s, отправим его в ближайшее время", channelName), nil)
		return
	}

	h.reply(chatID, "Собираем дайджест по всем каналам, отправим его в ближайшее время", nil)
}

func (h *Handler) handleClearRequest(chatID, tgUserID int64) {
	h.mu.Lock()
	h.pendingDrop[tgUserID] = time.Now()
	h.mu.Unlock()
	h.reply(chatID, "Отправьте /clear_data_confirm в течение 5 минут, чтобы удалить все данные.", nil)
}

func (h *Handler) handleClearConfirm(ctx context.Context, chatID, tgUserID int64) {
	h.mu.Lock()
	requested, ok := h.pendingDrop[tgUserID]
	if ok && time.Since(requested) > 5*time.Minute {
		delete(h.pendingDrop, tgUserID)
		ok = false
	}
	if ok {
		delete(h.pendingDrop, tgUserID)
	}
	h.mu.Unlock()
	if !ok {
		h.reply(chatID, "Запрос не найден. Сначала отправьте /clear_data", nil)
		return
	}
	user, err := h.users.GetByTGID(tgUserID)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось получить пользователя: %v", err), nil)
		return
	}
	if err := h.users.DeleteUserData(user.ID); err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось удалить данные: %v", err), nil)
		return
	}
	h.reply(chatID, "Данные удалены. Для продолжения отправьте /start", nil)
}

func parseID(data string) int64 {
	parts := strings.Split(data, ":")
	if len(parts) != 2 {
		return 0
	}
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	return id
}

func (h *Handler) reply(chatID int64, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	parts := telegram.SplitMessage(text)
	for i, part := range parts {
		msg := tgbotapi.NewMessage(chatID, part)
		if i == 0 && keyboard != nil {
			msg.ReplyMarkup = keyboard
		}
		if _, err := h.bot.Send(msg); err != nil {
			h.log.Error().Err(err).Msg("не удалось отправить сообщение")
			return
		}
	}
}

func (h *Handler) mainKeyboard() *tgbotapi.InlineKeyboardMarkup {
	buttons := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Добавить канал", "add_channel"),
			tgbotapi.NewInlineKeyboardButtonData("🕘 Настроить время", "set_time"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📚 Мои каналы", "my_channels"),
			tgbotapi.NewInlineKeyboardButtonData("📰 Получить дайджест", "digest_now"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("Открыть Mini App", "https://t.me"),
		),
	)
	return &buttons
}

// SchedulePresetKeyboard возвращает готовые кнопки выбора времени.
func SchedulePresetKeyboard() *tgbotapi.InlineKeyboardMarkup {
	row := tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("07:30", "set_time:07:30"),
		tgbotapi.NewInlineKeyboardButtonData("09:00", "set_time:09:00"),
		tgbotapi.NewInlineKeyboardButtonData("19:00", "set_time:19:00"),
	)
	markup := tgbotapi.NewInlineKeyboardMarkup(row)
	return &markup
}

// ParseLocalTime парсит время формата ЧЧ:ММ.
func ParseLocalTime(input string) (time.Time, error) {
	return time.Parse("15:04", input)
}
