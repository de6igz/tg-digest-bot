package bot

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog"

	"tg-digest-bot/internal/adapters/telegram"
	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/metrics"
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
	maxDigest   int
	mu          sync.Mutex
	pendingDrop map[int64]time.Time
	pendingTime map[int64]struct{}
}

// NewHandler создаёт обработчик.
func NewHandler(bot *tgbotapi.BotAPI, log zerolog.Logger, channelUC *channels.Service, scheduleUC *schedule.Service, userRepo domain.UserRepo, jobs domain.DigestQueue, maxDigest int) *Handler {
	return &Handler{
		bot:         bot,
		log:         log,
		channelUC:   channelUC,
		scheduleUC:  scheduleUC,
		users:       userRepo,
		jobs:        jobs,
		maxDigest:   maxDigest,
		pendingDrop: make(map[int64]time.Time),
		pendingTime: make(map[int64]struct{}),
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
	if msg.From != nil && !strings.HasPrefix(text, "/") {
		if h.tryHandleScheduleInput(ctx, msg.Chat.ID, msg.From.ID, text) {
			return
		}
	}
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
		if msg.From == nil {
			h.reply(msg.Chat.ID, "Не удалось определить пользователя", nil)
			return
		}
		payload := strings.TrimSpace(strings.TrimPrefix(text, "/schedule"))
		if payload == "" {
			h.handleSchedule(msg.Chat.ID, msg.From.ID)
			return
		}
		h.handleSetTime(ctx, msg.Chat.ID, msg.From.ID, payload)
	case strings.HasPrefix(text, "/tags"):
		h.handleTagsList(ctx, msg.Chat.ID, msg.From.ID)
	case strings.HasPrefix(text, "/tag"):
		payload := strings.TrimSpace(strings.TrimPrefix(text, "/tag"))
		h.handleTagCommand(ctx, msg.Chat.ID, msg.From.ID, payload)
	case strings.HasPrefix(text, "/digest_tag"):
		payload := strings.TrimSpace(strings.TrimPrefix(text, "/digest_tag"))
		h.handleDigestByTags(ctx, msg.Chat.ID, msg.From.ID, payload)
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
	user, err := h.users.UpsertByTGID(msg.From.ID, locale, "")
	if err != nil {
		h.reply(msg.Chat.ID, fmt.Sprintf("Ошибка сохранения профиля: %v", err), nil)
		return
	}
	h.reply(msg.Chat.ID, h.buildStartMessage(user.Plan()), h.mainKeyboard())
}

func (h *Handler) handleHelp(chatID int64) {
	h.reply(chatID, h.buildHelpMessage(), h.mainKeyboard())
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
			user, getErr := h.users.GetByTGID(tgUserID)
			if getErr != nil {
				h.reply(chatID, "Превышен лимит каналов для вашего тарифа.", nil)
				return
			}
			plan := user.Plan()
			if plan.ChannelLimit > 0 {
				h.reply(chatID, fmt.Sprintf("Тариф %s позволяет добавить до %d каналов. Удалите канал или обновите тариф.", plan.Name, plan.ChannelLimit), nil)
			} else {
				h.reply(chatID, "Для вашего тарифа нет ограничений по каналам, но произошла ошибка. Попробуйте позже.", nil)
			}
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
		line := fmt.Sprintf("%d. %s (@%s)", i+1, title, ch.Channel.Alias)
		if len(ch.Tags) > 0 {
			line += fmt.Sprintf(" — теги: %s", strings.Join(ch.Tags, ", "))
		}
		b.WriteString(line + "\n")
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

	tagCounters := make(map[string]int)
	for _, ch := range channels {
		for _, tag := range ch.Tags {
			trimmed := strings.TrimSpace(tag)
			if trimmed == "" {
				continue
			}
			tagCounters[trimmed]++
		}
	}
	if len(tagCounters) > 0 {
		tags := make([]string, 0, len(tagCounters))
		for tag := range tagCounters {
			tags = append(tags, tag)
		}
		sort.Strings(tags)
		for _, tag := range tags {
			encoded := url.QueryEscape(tag)
			label := fmt.Sprintf("🏷 %s (%d)", tag, tagCounters[tag])
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("digest_tag:%s", encoded)),
			))
		}
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	h.reply(chatID, "Выберите дайджест за последние 24 часа", &markup)
}

func (h *Handler) handleTagCommand(ctx context.Context, chatID, tgUserID int64, payload string) {
	if payload == "" {
		h.reply(chatID, "Используйте формат: /tag @alias новости, аналитика", nil)
		return
	}
	parts := strings.SplitN(payload, " ", 2)
	aliasInput := strings.TrimSpace(parts[0])
	if aliasInput == "" {
		h.reply(chatID, "Укажите алиас канала после команды", nil)
		return
	}
	parsed, err := channels.ParseAlias(aliasInput)
	if err != nil {
		h.reply(chatID, "Некорректный алиас", nil)
		return
	}
	var rawTags string
	if len(parts) > 1 {
		rawTags = parts[1]
	}
	tags := parseTagsInput(rawTags)

	list, err := h.channelUC.ListChannels(ctx, tgUserID, 100, 0)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Ошибка получения каналов: %v", err), nil)
		return
	}
	var (
		channelID int64
		title     string
	)
	for _, ch := range list {
		if strings.EqualFold(ch.Channel.Alias, parsed) {
			channelID = ch.ChannelID
			title = ch.Channel.Title
			if title == "" {
				title = ch.Channel.Alias
			}
			break
		}
	}
	if channelID == 0 {
		h.reply(chatID, "Канал не найден среди ваших подписок", nil)
		return
	}
	if err := h.channelUC.UpdateChannelTags(ctx, tgUserID, channelID, tags); err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось сохранить теги: %v", err), nil)
		return
	}
	if len(tags) == 0 {
		h.reply(chatID, fmt.Sprintf("Теги для %s очищены", title), nil)
		return
	}
	h.reply(chatID, fmt.Sprintf("Теги для %s обновлены: %s", title, strings.Join(tags, ", ")), nil)
}

func (h *Handler) handleTagsList(ctx context.Context, chatID, tgUserID int64) {
	channelsList, err := h.channelUC.ListChannels(ctx, tgUserID, 100, 0)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось получить каналы: %v", err), nil)
		return
	}
	if len(channelsList) == 0 {
		h.reply(chatID, "У вас пока нет каналов", nil)
		return
	}
	counter := make(map[string]int)
	for _, uc := range channelsList {
		for _, tag := range uc.Tags {
			trimmed := strings.TrimSpace(tag)
			if trimmed == "" {
				continue
			}
			counter[trimmed]++
		}
	}
	if len(counter) == 0 {
		h.reply(chatID, "У каналов пока нет тегов. Добавьте их командой /tag", nil)
		return
	}
	tags := make([]string, 0, len(counter))
	for tag := range counter {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	var b strings.Builder
	b.WriteString("Ваши теги:\n")
	for _, tag := range tags {
		b.WriteString(fmt.Sprintf("- %s — %d канал(а)\n", tag, counter[tag]))
	}
	b.WriteString("\nИспользуйте /digest_tag тег, чтобы получить дайджест.")
	h.reply(chatID, b.String(), nil)
}

func (h *Handler) handleDigestByTags(ctx context.Context, chatID, tgUserID int64, payload string) {
	tags := parseTagsInput(payload)
	if len(tags) == 0 {
		h.reply(chatID, "Укажите один или несколько тегов после команды", nil)
		return
	}
	channelsList, err := h.channelUC.ListChannels(ctx, tgUserID, 100, 0)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось получить каналы: %v", err), nil)
		return
	}
	if len(channelsList) == 0 {
		h.reply(chatID, "Сначала добавьте хотя бы один канал", nil)
		return
	}
	if !userHasTags(channelsList, tags) {
		h.reply(chatID, "Среди ваших каналов нет таких тегов", nil)
		return
	}
	h.enqueueDigestByTags(ctx, chatID, tgUserID, tags)
}

func (h *Handler) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	data := cb.Data
	switch {
	case data == "add_channel":
		h.reply(cb.Message.Chat.ID, "Отправьте /add @alias", nil)
	case data == "help_menu":
		h.reply(cb.Message.Chat.ID, h.buildHelpMessage(), h.mainKeyboard())
	case data == "digest_now":
		h.handleDigestNow(ctx, cb.Message.Chat.ID, cb.From.ID)
	case data == "digest_all":
		h.enqueueDigest(ctx, cb.Message.Chat.ID, cb.From.ID, 0)
	case strings.HasPrefix(data, "digest_channel:"):
		id := parseID(data)
		h.enqueueDigest(ctx, cb.Message.Chat.ID, cb.From.ID, id)
	case data == "digest_tag_menu":
		h.reply(cb.Message.Chat.ID, h.buildTagDigestHint(), nil)
	case strings.HasPrefix(data, "digest_tag:"):
		encoded := strings.TrimPrefix(data, "digest_tag:")
		tag, err := url.QueryUnescape(encoded)
		if err != nil {
			h.reply(cb.Message.Chat.ID, "Не удалось распознать тег", nil)
			return
		}
		h.enqueueDigestByTags(ctx, cb.Message.Chat.ID, cb.From.ID, []string{tag})
	case data == "my_channels":
		h.handleList(ctx, cb.Message.Chat.ID, cb.From.ID)
	case data == "tags_list":
		h.handleTagsList(ctx, cb.Message.Chat.ID, cb.From.ID)
	case data == "set_time":
		h.handleSchedule(cb.Message.Chat.ID, cb.From.ID)
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
	start := time.Now()
	_, err := h.bot.Request(tgbotapi.NewCallback(cb.ID, ""))
	metrics.ObserveNetworkRequest("telegram_bot", "answer_callback", strconv.FormatInt(cb.From.ID, 10), start, err)
	if err != nil {
		h.log.Error().Err(err).Msg("не удалось ответить на callback")
	}
}

func (h *Handler) handleSchedule(chatID, tgUserID int64) {
	user, err := h.users.GetByTGID(tgUserID)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось получить профиль: %v", err), nil)
		return
	}
	h.setPendingSchedule(tgUserID)
	current := user.DailyTime.Format("15:04")
	tzSuffix := ""
	if user.Timezone != "" {
		tzSuffix = fmt.Sprintf(" (%s)", user.Timezone)
	}
	message := []string{
		fmt.Sprintf("Текущее время ежедневной рассылки: %s%s.", current, tzSuffix),
		"",
		"Выберите подходящий вариант ниже или укажите своё время.",
		"Можно просто отправить 21:30 или воспользоваться командой /schedule 21:30.",
		"Формат — ЧЧ:ММ, 24-часовой.",
	}
	h.reply(chatID, strings.Join(message, "\n"), SchedulePresetKeyboard())
}

func (h *Handler) handleSetTime(ctx context.Context, chatID, tgUserID int64, value string) {
	value = strings.TrimSpace(value)
	tm, err := ParseLocalTime(value)
	if err != nil {
		h.reply(chatID, "Некорректный формат времени. Используйте ЧЧ:ММ", nil)
		return
	}
	if err := h.scheduleUC.UpdateDailyTime(ctx, tgUserID, tm); err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось сохранить время: %v", err), nil)
		return
	}
	h.clearPendingSchedule(tgUserID)
	h.reply(chatID, fmt.Sprintf("Время доставки установлено на %s по вашему локальному времени", tm.Format("15:04")), nil)
}

func (h *Handler) tryHandleScheduleInput(ctx context.Context, chatID, tgUserID int64, value string) bool {
	h.mu.Lock()
	_, pending := h.pendingTime[tgUserID]
	h.mu.Unlock()
	if !pending {
		return false
	}
	if strings.TrimSpace(value) == "" {
		h.reply(chatID, "Отправьте время в формате ЧЧ:ММ, например 21:30", nil)
		return true
	}
	h.handleSetTime(ctx, chatID, tgUserID, value)
	return true
}

func (h *Handler) setPendingSchedule(tgUserID int64) {
	h.mu.Lock()
	h.pendingTime[tgUserID] = struct{}{}
	h.mu.Unlock()
}

func (h *Handler) clearPendingSchedule(tgUserID int64) {
	h.mu.Lock()
	delete(h.pendingTime, tgUserID)
	h.mu.Unlock()
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

func (h *Handler) reserveManualRequest(chatID int64, user domain.User) (domain.ManualRequestState, bool) {
	state, err := h.users.ReserveManualRequest(user.ID, time.Now().UTC())
	if err != nil {
		h.log.Error().Err(err).Int64("user", user.TGUserID).Msg("не удалось зарезервировать ручной запрос")
		h.reply(chatID, "Не удалось обработать запрос. Попробуйте позже.", nil)
		return domain.ManualRequestState{}, false
	}
	if !state.Allowed {
		h.replyManualLimit(chatID, state)
		return state, false
	}
	return state, true
}

func (h *Handler) replyManualLimit(chatID int64, state domain.ManualRequestState) {
	var lines []string
	lines = append(lines, fmt.Sprintf("Вы достигли лимита запросов для тарифа %s.", state.Plan.Name))
	switch {
	case state.Plan.ManualDailyLimit <= 0:
		lines = append(lines, "Лимитов для этого тарифа нет, попробуйте повторить запрос позже или обратитесь в поддержку.")
	case state.Plan.Role == domain.UserRoleFree && state.Plan.ManualIntroTotal > 0:
		lines = append(lines, fmt.Sprintf("После первых %d запросов доступен %d запрос в сутки.", state.Plan.ManualIntroTotal, state.Plan.ManualDailyLimit))
		lines = append(lines, "Попробуйте завтра или обновите тариф.")
	default:
		lines = append(lines, fmt.Sprintf("Лимит — %d запросов в сутки. Попробуйте завтра или обновите тариф.", state.Plan.ManualDailyLimit))
	}
	h.reply(chatID, strings.Join(lines, "\n"), nil)
}

func (h *Handler) enqueueDigest(ctx context.Context, chatID, tgUserID, channelID int64) {
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

	user, err := h.users.GetByTGID(tgUserID)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось получить профиль: %v", err), nil)
		return
	}
	if _, ok := h.reserveManualRequest(chatID, user); !ok {
		return
	}

	now := time.Now().UTC()
	job := domain.DigestJob{
		UserTGID:    tgUserID,
		ChatID:      chatID,
		ChannelID:   channelID,
		Date:        now,
		RequestedAt: now,
		Cause:       domain.DigestCauseManual,
	}

	if err := h.jobs.Enqueue(ctx, job); err != nil {
		h.log.Error().Err(err).Int64("user", tgUserID).Int64("channel", channelID).Msg("не удалось поставить задачу дайджеста")
		h.reply(chatID, "Не удалось поставить дайджест в очередь, попробуйте позже", nil)
		return
	}

	metrics.IncDigestOverall()
	metrics.IncDigestForUser(tgUserID)
	if channelID > 0 {
		metrics.IncDigestForChannel(channelID)
	}

	if channelID > 0 {
		h.reply(chatID, fmt.Sprintf("Собираем дайджест по каналу %s, отправим его в ближайшее время", channelName), nil)
		return
	}

	h.reply(chatID, "Собираем дайджест по всем каналам, отправим его в ближайшее время", nil)
}

func (h *Handler) enqueueDigestByTags(ctx context.Context, chatID, tgUserID int64, tags []string) {
	cleaned := channels.NormalizeTags(tags)
	if len(cleaned) == 0 {
		h.reply(chatID, "Укажите теги для дайджеста", nil)
		return
	}
	user, err := h.users.GetByTGID(tgUserID)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось получить профиль: %v", err), nil)
		return
	}
	if _, ok := h.reserveManualRequest(chatID, user); !ok {
		return
	}
	now := time.Now().UTC()
	job := domain.DigestJob{
		UserTGID:    tgUserID,
		ChatID:      chatID,
		Tags:        cleaned,
		Date:        now,
		RequestedAt: now,
		Cause:       domain.DigestCauseManual,
	}
	if err := h.jobs.Enqueue(ctx, job); err != nil {
		h.log.Error().Err(err).Int64("user", tgUserID).Strs("tags", cleaned).Msg("не удалось поставить задачу дайджеста")
		h.reply(chatID, "Не удалось поставить дайджест в очередь, попробуйте позже", nil)
		return
	}
	metrics.IncDigestOverall()
	metrics.IncDigestForUser(tgUserID)
	h.reply(chatID, fmt.Sprintf("Собираем дайджест по тегам: %s", strings.Join(cleaned, ", ")), nil)
}

func parseTagsInput(input string) []string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil
	}
	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		switch r {
		case ',', ';', '\n':
			return true
		default:
			return false
		}
	})
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		tag := strings.TrimSpace(part)
		if tag == "" {
			continue
		}
		tags = append(tags, tag)
	}
	return channels.NormalizeTags(tags)
}

func userHasTags(channelsList []domain.UserChannel, tags []string) bool {
	if len(tags) == 0 {
		return false
	}
	lookup := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		lookup[strings.ToLower(strings.TrimSpace(tag))] = struct{}{}
	}
	for _, ch := range channelsList {
		for _, tag := range ch.Tags {
			trimmed := strings.TrimSpace(tag)
			if trimmed == "" {
				continue
			}
			if _, ok := lookup[strings.ToLower(trimmed)]; ok {
				return true
			}
		}
	}
	return false
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
		start := time.Now()
		_, err := h.bot.Send(msg)
		metrics.ObserveNetworkRequest("telegram_bot", "send_message", strconv.FormatInt(chatID, 10), start, err)
		if err != nil {
			h.log.Error().Err(err).Msg("не удалось отправить сообщение")
			return
		}
	}
}

func (h *Handler) mainKeyboard() *tgbotapi.InlineKeyboardMarkup {
	buttons := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Добавить канал", "add_channel"),
			tgbotapi.NewInlineKeyboardButtonData("📚 Мои каналы", "my_channels"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📰 Дайджест", "digest_now"),
			tgbotapi.NewInlineKeyboardButtonData("📌 Дайджест по тегам", "digest_tag_menu"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏷 Теги каналов", "tags_list"),
			tgbotapi.NewInlineKeyboardButtonData("🗓 Расписание", "set_time"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("ℹ️ Помощь", "help_menu"),
		),
	)
	return &buttons
}

func (h *Handler) buildStartMessage(plan domain.UserPlan) string {
	limitLine := "   Вам доступно неограниченное количество каналов."
	if plan.ChannelLimit > 0 {
		limitLine = fmt.Sprintf("   Вам доступно до %d каналов.", plan.ChannelLimit)
	}
	requestLine := "   Ручные дайджесты не ограничены."
	switch {
	case plan.ManualDailyLimit <= 0:
		requestLine = "   Ручные дайджесты не ограничены."
	case plan.ManualIntroTotal > 0:
		requestLine = fmt.Sprintf("   Первые %d запросов мгновенно, далее до %d в сутки.", plan.ManualIntroTotal, plan.ManualDailyLimit)
	default:
		requestLine = fmt.Sprintf("   Лимит ручных дайджестов — %d в сутки.", plan.ManualDailyLimit)
	}
	lines := []string{
		"👋 Добро пожаловать в TG Digest Bot!",
		"",
		fmt.Sprintf("Ваш тариф: %s.", plan.Name),
		"",
		"Как пользоваться ботом:",
		"1. ➕ Добавьте канал — кнопка \"Добавить канал\" или команда /add @alias.",
		limitLine,
		"2. 🏷 Назначьте теги: /tag @alias новости, аналитика.",
		"3. 📰 Соберите дайджест за последние 24 часа — кнопка \"Дайджест\" или команда /digest_now.",
		"   Чтобы получить дайджест по темам, используйте /digest_tag новости.",
		requestLine,
		"4. 🗓 Настройте автоматическую рассылку — кнопка \"Расписание\" или команда /schedule 21:30.",
		"",
		"Под кнопкой \"ℹ️ Помощь\" вы найдёте полный список команд и примеров.",
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) buildHelpMessage() string {
	sections := []string{
		"📖 Основные команды и примеры:",
		"",
		"Управление каналами:",
		"• /add @toporlive — добавить канал.",
		"• /list — показать сохранённые каналы и действия с ними.",
		"• /mute @toporlive — временно убрать канал из дайджеста.",
		"• /unmute @toporlive — вернуть канал в дайджест.",
		"• /tag @toporlive новости, аналитика — задать теги.",
		"• /tags — посмотреть список ваших тегов.",
		"",
		"Дайджесты:",
		"• /digest_now — собрать дайджест из всех немьютнутых каналов.",
		"• /digest_tag новости — дайджест только по каналам с тегом \"новости\".",
		"",
		"Расписание и данные:",
		"• /schedule — открыть выбор времени.",
		"• /schedule 21:30 — задать своё время рассылки.",
		"• /clear_data — удалить аккаунт и все сохранённые данные.",
		"",
		"Подсказка: используйте меню под сообщением, чтобы быстро перейти к нужному действию.",
	}
	return strings.Join(sections, "\n")
}

func (h *Handler) buildTagDigestHint() string {
	lines := []string{
		"📌 Как получить дайджест по тегам:",
		"1. Задайте теги каналу: /tag @toporlive новости, аналитика.",
		"2. Посмотрите доступные теги через кнопку \"🏷 Теги каналов\" или команду /tags.",
		"3. Запросите подборку: /digest_tag новости или несколько тегов через запятую.",
		"",
		"Совет: теги не чувствительны к регистру, но старайтесь писать их одинаково, чтобы группировать каналы по темам.",
	}
	return strings.Join(lines, "\n")
}

// SchedulePresetKeyboard возвращает готовые кнопки выбора времени.
func SchedulePresetKeyboard() *tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("07:30", "set_time:07:30"),
			tgbotapi.NewInlineKeyboardButtonData("09:00", "set_time:09:00"),
			tgbotapi.NewInlineKeyboardButtonData("12:00", "set_time:12:00"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("18:00", "set_time:18:00"),
			tgbotapi.NewInlineKeyboardButtonData("19:00", "set_time:19:00"),
			tgbotapi.NewInlineKeyboardButtonData("21:00", "set_time:21:00"),
		),
	}
	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return &markup
}

// ParseLocalTime парсит время формата ЧЧ:ММ.
func ParseLocalTime(input string) (time.Time, error) {
	return time.Parse("15:04", strings.TrimSpace(input))
}
