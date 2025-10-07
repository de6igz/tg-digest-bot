package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog"

	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/usecase/channels"
)

// Handler обслуживает вебхук бота.
type Handler struct {
	bot        *tgbotapi.BotAPI
	log        zerolog.Logger
	channelUC  *channels.Service
	digestSrv  domain.DigestService
	scheduleUC domain.UserRepo
	freeLimit  int
	maxDigest  int
}

// NewHandler создаёт обработчик.
func NewHandler(bot *tgbotapi.BotAPI, log zerolog.Logger, channelUC *channels.Service, digestSrv domain.DigestService, userRepo domain.UserRepo, freeLimit, maxDigest int) *Handler {
	return &Handler{bot: bot, log: log, channelUC: channelUC, digestSrv: digestSrv, scheduleUC: userRepo, freeLimit: freeLimit, maxDigest: maxDigest}
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
		h.reply(msg.Chat.ID, "👋 Добро пожаловать!", h.mainKeyboard())
	case strings.HasPrefix(text, "/help"):
		h.reply(msg.Chat.ID, "Команды: \n/add \n/list \n/digest_now \n/schedule \n/mute \n/unmute \n/clear_data", nil)
	case strings.HasPrefix(text, "/add"):
		alias := strings.TrimSpace(strings.TrimPrefix(text, "/add"))
		h.handleAdd(ctx, msg.Chat.ID, msg.From.ID, alias)
	case strings.HasPrefix(text, "/list"):
		h.handleList(ctx, msg.Chat.ID, msg.From.ID)
	case strings.HasPrefix(text, "/digest_now"):
		h.handleDigestNow(ctx, msg.Chat.ID, msg.From.ID)
	case strings.HasPrefix(text, "/schedule"):
		h.reply(msg.Chat.ID, "Выберите время: 07:30 / 09:00 / 19:00", nil)
	case strings.HasPrefix(text, "/clear_data"):
		h.reply(msg.Chat.ID, "Чтобы удалить данные отправьте /clear_data_confirm", nil)
	default:
		h.reply(msg.Chat.ID, "Неизвестная команда. Используйте /help", nil)
	}
}

func (h *Handler) handleAdd(ctx context.Context, chatID int64, tgUserID int64, alias string) {
	if alias == "" {
		h.reply(chatID, "Отправьте /add @alias", nil)
		return
	}
	channel, err := h.channelUC.AddChannel(ctx, tgUserID, alias)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Ошибка: %v", err), nil)
		return
	}
	h.reply(chatID, fmt.Sprintf("Готово: %s", channel.Title), nil)
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
		fmt.Fprintf(&b, "%d. %s (@%s)\n", i+1, ch.Title, ch.Alias)
	}
	h.reply(chatID, b.String(), nil)
}

func (h *Handler) handleDigestNow(ctx context.Context, chatID int64, tgUserID int64) {
	if err := h.digestSrv.BuildAndSendNow(tgUserID); err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось собрать дайджест: %v", err), nil)
		return
	}
	h.reply(chatID, "Дайджест отправлен!", nil)
}

func (h *Handler) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	data := cb.Data
	switch {
	case data == "digest_now":
		h.handleDigestNow(ctx, cb.Message.Chat.ID, cb.From.ID)
	case strings.HasPrefix(data, "mute:"):
		id := parseID(data)
		h.channelUC.ToggleMute(ctx, cb.From.ID, id, true)
	case strings.HasPrefix(data, "unmute:"):
		id := parseID(data)
		h.channelUC.ToggleMute(ctx, cb.From.ID, id, false)
	}
	_, _ = h.bot.Request(tgbotapi.NewCallback(cb.ID, ""))
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
	msg := tgbotapi.NewMessage(chatID, text)
	if keyboard != nil {
		msg.ReplyMarkup = keyboard
	}
	if _, err := h.bot.Send(msg); err != nil {
		h.log.Error().Err(err).Msg("не удалось отправить сообщение")
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
