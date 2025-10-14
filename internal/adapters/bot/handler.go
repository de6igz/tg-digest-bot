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
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"tg-digest-bot/internal/adapters/telegram"
	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/metrics"
	billingusecase "tg-digest-bot/internal/usecase/billing"
	"tg-digest-bot/internal/usecase/channels"
	"tg-digest-bot/internal/usecase/schedule"
)

// Handler обслуживает вебхук бота.
type Handler struct {
	bot          *tgbotapi.BotAPI
	log          zerolog.Logger
	channelUC    *channels.Service
	scheduleUC   *schedule.Service
	users        domain.UserRepo
	billing      domain.Billing
	sbp          *billingusecase.Service
	jobs         domain.DigestQueue
	maxDigest    int
	mu           sync.Mutex
	pendingDrop  map[int64]time.Time
	pendingTime  map[int64]struct{}
	sbpNotifyURL string
	offers       map[string]subscriptionOffer
}

// NewHandler создаёт обработчик.
func NewHandler(bot *tgbotapi.BotAPI, log zerolog.Logger, channelUC *channels.Service, scheduleUC *schedule.Service, userRepo domain.UserRepo, billing domain.Billing, sbpService *billingusecase.Service, jobs domain.DigestQueue, maxDigest int, sbpNotifyURL string) *Handler {
	return &Handler{
		bot:          bot,
		log:          log,
		channelUC:    channelUC,
		scheduleUC:   scheduleUC,
		users:        userRepo,
		billing:      billing,
		sbp:          sbpService,
		jobs:         jobs,
		maxDigest:    maxDigest,
		pendingDrop:  make(map[int64]time.Time),
		pendingTime:  make(map[int64]struct{}),
		sbpNotifyURL: sbpNotifyURL,
		offers:       defaultSubscriptionOffers(),
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
	case strings.HasPrefix(text, "/balance"):
		if msg.From == nil {
			h.reply(msg.Chat.ID, "Не удалось определить пользователя", nil)
			return
		}
		h.handleBalance(ctx, msg.Chat.ID, msg.From.ID)
	case strings.HasPrefix(text, "/deposit"):
		if msg.From == nil {
			h.reply(msg.Chat.ID, "Не удалось определить пользователя", nil)
			return
		}
		amount := strings.TrimSpace(strings.TrimPrefix(text, "/deposit"))
		h.handleDeposit(ctx, msg.Chat.ID, msg.From.ID, amount)
	case strings.HasPrefix(text, "/buy"):
		if msg.From == nil {
			h.reply(msg.Chat.ID, "Не удалось определить пользователя", nil)
			return
		}
		plan := strings.TrimSpace(strings.TrimPrefix(text, "/buy"))
		h.handleBuySubscription(ctx, msg.Chat.ID, msg.From.ID, plan)
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
	user, created, err := h.users.UpsertByTGID(msg.From.ID, locale, "")
	if err != nil {
		h.reply(msg.Chat.ID, fmt.Sprintf("Ошибка сохранения профиля: %v", err), nil)
		return
	}
	payload := ""
	if msg.Text != "" {
		fields := strings.Fields(msg.Text)
		if len(fields) > 1 {
			payload = fields[1]
		}
	}
	var referralResult domain.ReferralResult
	if payload != "" && (created || user.ReferredByID == nil) {
		result, applyErr := h.users.ApplyReferral(payload, user.ID)
		if applyErr != nil {
			h.log.Error().Err(applyErr).Int64("user", msg.From.ID).Msg("не удалось применить реферальный код")
		} else {
			user = result.User
			referralResult = result
		}
	}

	sections := h.buildStartSections(user)
	for i, section := range sections {
		if strings.TrimSpace(section) == "" {
			continue
		}
		if i == 0 {
			h.reply(msg.Chat.ID, section, h.mainKeyboard())
			continue
		}
		h.reply(msg.Chat.ID, section, nil)
	}

	if referralResult.ReferrerUpgraded && referralResult.Referrer != nil {
		h.notifyPlanUpgrade(*referralResult.Referrer, referralResult.PreviousRole)
	}
}

func (h *Handler) handleHelp(chatID int64) {
	h.reply(chatID, h.buildHelpMessage(), h.mainKeyboard())
}

func (h *Handler) handleBalance(ctx context.Context, chatID, tgUserID int64) {
	if tgUserID == 0 {
		h.reply(chatID, "Не удалось определить пользователя", nil)
		return
	}
	if h.billing == nil {
		h.reply(chatID, "Биллинг временно недоступен. Попробуйте позже.", nil)
		return
	}
	user, err := h.users.GetByTGID(tgUserID)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось получить профиль: %v", err), nil)
		return
	}
	account, err := h.billing.EnsureAccount(ctx, user.ID)
	if err != nil {
		h.log.Error().Err(err).Int64("user", tgUserID).Msg("billing: ensure account failed")
		h.reply(chatID, "Не удалось получить баланс. Попробуйте позже.", nil)
		return
	}
	balanceText := formatMoney(account.Balance.Amount, account.Balance.Currency)
	lines := []string{
		"💳 Баланс счёта:",
		fmt.Sprintf("• %s", balanceText),
		"",
		"Пополните баланс командой /deposit 500 или через кнопки ниже.",
	}
	h.reply(chatID, strings.Join(lines, "\n"), h.balanceKeyboard())
}

func (h *Handler) handleDeposit(ctx context.Context, chatID, tgUserID int64, payload string) {
	if tgUserID == 0 {
		h.reply(chatID, "Не удалось определить пользователя", nil)
		return
	}
	if h.billing == nil || h.sbp == nil {
		h.reply(chatID, "Пополнение временно недоступно. Попробуйте позже.", nil)
		return
	}
	user, err := h.users.GetByTGID(tgUserID)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось получить профиль: %v", err), nil)
		return
	}
	amountText := strings.TrimSpace(payload)
	if amountText == "" {
		h.sendTopUpMenu(chatID)
		return
	}
	amountMinor, err := parseAmountToMinor(amountText)
	if err != nil || amountMinor <= 0 {
		h.reply(chatID, "Укажите сумму в рублях, например /deposit 500 или /deposit 249.99", h.topUpPresetKeyboard())
		return
	}
	if amountMinor < 100 {
		h.reply(chatID, "Минимальная сумма пополнения — 1 ₽.", h.topUpPresetKeyboard())
		return
	}
	account, err := h.billing.EnsureAccount(ctx, user.ID)
	if err != nil {
		h.log.Error().Err(err).Int64("user", tgUserID).Msg("billing: ensure account failed")
		h.reply(chatID, "Не удалось создать счёт для пополнения. Попробуйте позже.", nil)
		return
	}
	currency := account.Balance.Currency
	if currency == "" {
		currency = "RUB"
	}
	description := fmt.Sprintf("Пополнение баланса TG Digest Bot на %s", formatMoney(amountMinor, currency))
	idempotencyKey := uuid.NewString()
	metadata := map[string]any{
		"type":        "topup",
		"source":      "telegram_bot",
		"user_id":     user.ID,
		"tg_user_id":  tgUserID,
		"description": description,
	}
	params := billingusecase.CreateSBPInvoiceParams{
		UserID:         user.ID,
		Amount:         domain.Money{Amount: amountMinor, Currency: currency},
		Description:    description,
		PaymentPurpose: fmt.Sprintf("Пополнение TG Digest Bot для пользователя %d", user.ID),
		IdempotencyKey: idempotencyKey,
		Metadata:       metadata,
		Extra: map[string]any{
			"user_id":    user.ID,
			"tg_user_id": tgUserID,
			"source":     "telegram_bot",
		},
	}
	if h.sbpNotifyURL != "" {
		params.NotificationURL = h.sbpNotifyURL
	}
	result, err := h.sbp.CreateInvoiceWithQRCode(ctx, params)
	if err != nil {
		h.log.Error().Err(err).Int64("user", tgUserID).Msg("billing: create sbp invoice failed")
		h.reply(chatID, "Не удалось создать счёт для пополнения. Попробуйте позже.", nil)
		return
	}
	amountFmt := formatMoney(result.Invoice.Amount.Amount, result.Invoice.Amount.Currency)
	lines := []string{
		"🧾 Счёт на пополнение создан.",
		fmt.Sprintf("Сумма: %s.", amountFmt),
	}
	if result.QR.PaymentLink != "" {
		lines = append(lines, fmt.Sprintf("Ссылка на оплату: %s", result.QR.PaymentLink))
	}
	if result.QR.ExpiresAt != nil {
		lines = append(lines, fmt.Sprintf("Счёт действует до %s.", result.QR.ExpiresAt.Local().Format("02.01.2006 15:04")))
	}
	lines = append(lines,
		"",
		"Оплатите счёт в приложении банка. Баланс обновится автоматически после поступления денег.",
	)
	h.reply(chatID, strings.Join(lines, "\n"), h.topUpInvoiceKeyboard(result.QR.PaymentLink))
}

func (h *Handler) handleBuySubscription(ctx context.Context, chatID, tgUserID int64, payload string) {
	if tgUserID == 0 {
		h.reply(chatID, "Не удалось определить пользователя", nil)
		return
	}
	if h.billing == nil {
		h.reply(chatID, "Биллинг временно недоступен. Попробуйте позже.", nil)
		return
	}
	user, err := h.users.GetByTGID(tgUserID)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось получить профиль: %v", err), nil)
		return
	}
	planKey := strings.ToLower(strings.TrimSpace(payload))
	if planKey == "" {
		h.sendSubscriptionMenu(chatID, user)
		return
	}
	offer, ok := h.offers[planKey]
	if !ok {
		h.reply(chatID, "Укажите тариф: /buy plus или /buy pro.", h.subscriptionKeyboard(user))
		return
	}
	if planPriority(user.Role) >= planPriority(offer.Role) {
		h.reply(chatID, fmt.Sprintf("У вас уже активен тариф %s или выше.", domain.PlanForRole(user.Role).Name), h.subscriptionKeyboard(user))
		return
	}
	account, err := h.billing.EnsureAccount(ctx, user.ID)
	if err != nil {
		h.log.Error().Err(err).Int64("user", tgUserID).Msg("billing: ensure account failed")
		h.reply(chatID, "Не удалось проверить баланс. Попробуйте позже.", nil)
		return
	}
	currency := account.Balance.Currency
	if currency == "" {
		currency = "RUB"
	}
	if account.Balance.Amount < offer.PriceMinor {
		shortage := offer.PriceMinor - account.Balance.Amount
		lines := []string{
			"Недостаточно средств на счёте для покупки подписки.",
			fmt.Sprintf("Нужно ещё %s.", formatMoney(shortage, currency)),
		}
		h.reply(chatID, strings.Join(lines, "\n"), h.topUpPresetKeyboard())
		return
	}
	metadata := map[string]any{
		"type":       "subscription_charge",
		"plan":       offer.Key,
		"plan_name":  offer.Title,
		"user_id":    user.ID,
		"tg_user_id": tgUserID,
		"duration":   offer.Duration,
	}
	description := fmt.Sprintf("Подписка %s", offer.Title)
	payment, err := h.billing.ChargeAccount(ctx, domain.ChargeAccountParams{
		AccountID:      account.ID,
		Amount:         domain.Money{Amount: offer.PriceMinor, Currency: currency},
		Description:    description,
		Metadata:       metadata,
		IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		if errors.Is(err, domain.ErrInsufficientFunds) {
			h.reply(chatID, "Недостаточно средств на счёте. Пополните баланс командой /deposit 500.", h.topUpPresetKeyboard())
			return
		}
		h.log.Error().Err(err).Int64("user", tgUserID).Msg("billing: charge account failed")
		h.reply(chatID, "Не удалось списать оплату. Попробуйте позже или обратитесь в поддержку.", nil)
		return
	}
	if err := h.users.UpdateRole(user.ID, offer.Role); err != nil {
		h.log.Error().Err(err).Int64("user", tgUserID).Msg("billing: update role failed")
		h.reply(chatID, "Оплата прошла, но не удалось активировать подписку. Напишите в поддержку, мы всё исправим.", nil)
		return
	}
	user.Role = offer.Role
	plan := user.Plan()
	channelLine, manualLine := h.mainPlanLines(plan)
	balance, balErr := h.billing.GetAccountByUserID(ctx, user.ID)
	if balErr != nil {
		h.log.Error().Err(balErr).Int64("user", tgUserID).Msg("billing: get balance after charge")
	}
	lines := []string{
		fmt.Sprintf("✅ Подписка %s активирована!", offer.Title),
		fmt.Sprintf("Списано: %s (платёж #%d).", formatMoney(offer.PriceMinor, currency), payment.ID),
		fmt.Sprintf("Доступ действует: %s.", offer.Duration),
		"",
		"Новые лимиты:",
		fmt.Sprintf("• %s", channelLine),
		fmt.Sprintf("• %s", manualLine),
	}
	if balErr == nil {
		lines = append(lines, "", fmt.Sprintf("Текущий баланс: %s.", formatMoney(balance.Balance.Amount, balance.Balance.Currency)))
	}
	lines = append(lines, "", "Спасибо, что поддерживаете проект!")
	h.reply(chatID, strings.Join(lines, "\n"), h.balanceKeyboard())
}

func (h *Handler) sendTopUpMenu(chatID int64) {
	lines := []string{
		"💰 Пополнение баланса:",
		"Выберите сумму ниже или отправьте команду /deposit 500 для пополнения на 500 ₽.",
		"Можно указать копейки через точку, например /deposit 249.99.",
	}
	h.reply(chatID, strings.Join(lines, "\n"), h.topUpPresetKeyboard())
}

func (h *Handler) sendSubscriptionMenu(chatID int64, user domain.User) {
	offers := h.subscriptionOffersOrdered()
	if len(offers) == 0 {
		h.reply(chatID, "Подписки временно недоступны.", nil)
		return
	}
	var lines []string
	lines = append(lines, "🛒 Доступные подписки:")
	available := 0
	for _, offer := range offers {
		if planPriority(user.Role) >= planPriority(offer.Role) {
			continue
		}
		available++
		price := formatMoney(offer.PriceMinor, "RUB")
		lines = append(lines,
			"",
			fmt.Sprintf("• %s — %s (%s)", offer.Title, price, offer.Duration),
		)
		for _, bullet := range offer.Bullets {
			lines = append(lines, fmt.Sprintf("   ◦ %s", bullet))
		}
		lines = append(lines, fmt.Sprintf("   Команда: /buy %s", offer.Key))
	}
	if available == 0 {
		lines = append(lines, "", "Вы уже используете максимальный тариф. Спасибо, что поддерживаете проект!")
		h.reply(chatID, strings.Join(lines, "\n"), nil)
		return
	}
	lines = append(lines,
		"",
		"Для оформления подпишитесь через кнопку ниже или пополните баланс командой /deposit сумма.",
	)
	h.reply(chatID, strings.Join(lines, "\n"), h.subscriptionKeyboard(user))
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
	case data == "plan_info":
		h.sendPlanInfo(cb.Message.Chat.ID, cb.From.ID)
	case data == "referral_info":
		h.sendReferralInfo(cb.Message.Chat.ID, cb.From.ID)
	case data == "billing_balance":
		h.handleBalance(ctx, cb.Message.Chat.ID, cb.From.ID)
	case data == "billing_topup":
		h.handleDeposit(ctx, cb.Message.Chat.ID, cb.From.ID, "")
	case strings.HasPrefix(data, "billing_topup:"):
		amount := strings.TrimPrefix(data, "billing_topup:")
		h.handleDeposit(ctx, cb.Message.Chat.ID, cb.From.ID, amount)
	case data == "billing_subscribe":
		h.handleBuySubscription(ctx, cb.Message.Chat.ID, cb.From.ID, "")
	case strings.HasPrefix(data, "plan_buy:"):
		planKey := strings.TrimPrefix(data, "plan_buy:")
		h.handleBuySubscription(ctx, cb.Message.Chat.ID, cb.From.ID, planKey)
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

func (h *Handler) subscriptionOffersOrdered() []subscriptionOffer {
	offers := make([]subscriptionOffer, 0, len(h.offers))
	for _, offer := range h.offers {
		offers = append(offers, offer)
	}
	sort.Slice(offers, func(i, j int) bool {
		if offers[i].PriceMinor == offers[j].PriceMinor {
			return planPriority(offers[i].Role) < planPriority(offers[j].Role)
		}
		return offers[i].PriceMinor < offers[j].PriceMinor
	})
	return offers
}

func (h *Handler) balanceKeyboard() *tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💰 Пополнить", "billing_topup"),
			tgbotapi.NewInlineKeyboardButtonData("🛒 Подписка", "billing_subscribe"),
		),
	}
	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return &markup
}

func (h *Handler) topUpPresetKeyboard() *tgbotapi.InlineKeyboardMarkup {
	if len(defaultTopUpPresets) == 0 {
		return h.balanceKeyboard()
	}
	row := make([]tgbotapi.InlineKeyboardButton, 0, len(defaultTopUpPresets))
	for _, amount := range defaultTopUpPresets {
		label, payload := presetButtonData(amount)
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(label, "billing_topup:"+payload))
	}
	rows := [][]tgbotapi.InlineKeyboardButton{row,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💳 Баланс", "billing_balance"),
		),
	}
	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return &markup
}

func (h *Handler) topUpInvoiceKeyboard(link string) *tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	if strings.TrimSpace(link) != "" {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("🔗 Оплатить", link),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("💳 Баланс", "billing_balance"),
		tgbotapi.NewInlineKeyboardButtonData("🛒 Подписка", "billing_subscribe"),
	))
	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return &markup
}

func (h *Handler) subscriptionKeyboard(user domain.User) *tgbotapi.InlineKeyboardMarkup {
	offers := h.subscriptionOffersOrdered()
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, offer := range offers {
		if planPriority(user.Role) >= planPriority(offer.Role) {
			continue
		}
		label := fmt.Sprintf("%s — %s", offer.Title, formatMoney(offer.PriceMinor, "RUB"))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, "plan_buy:"+offer.Key),
		))
	}
	if len(rows) == 0 {
		return nil
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("💰 Пополнить", "billing_topup"),
		tgbotapi.NewInlineKeyboardButtonData("💳 Баланс", "billing_balance"),
	))
	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return &markup
}

func planPriority(role domain.UserRole) int {
	switch role {
	case domain.UserRoleFree:
		return 0
	case domain.UserRolePlus:
		return 1
	case domain.UserRolePro:
		return 2
	case domain.UserRoleDeveloper:
		return 3
	default:
		return -1
	}
}

func formatMoney(amount int64, currency string) string {
	sign := ""
	if amount < 0 {
		sign = "-"
		amount = -amount
	}
	major := amount / 100
	minor := amount % 100
	symbol := currencySymbol(currency)
	return fmt.Sprintf("%s%d.%02d %s", sign, major, minor, symbol)
}

func currencySymbol(currency string) string {
	trimmed := strings.TrimSpace(strings.ToUpper(currency))
	switch trimmed {
	case "RUB", "RUR":
		return "₽"
	case "":
		return "RUB"
	default:
		return trimmed
	}
}

func presetButtonData(amount int64) (label string, payload string) {
	label = formatMoney(amount, "RUB")
	major := amount / 100
	minor := amount % 100
	payload = fmt.Sprintf("%d.%02d", major, minor)
	payload = strings.TrimRight(strings.TrimRight(payload, "0"), ".")
	if payload == "" {
		payload = "0"
	}
	return label, payload
}

func parseAmountToMinor(input string) (int64, error) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(input, ",", "."))
	if trimmed == "" {
		return 0, fmt.Errorf("amount is required")
	}
	if strings.HasPrefix(trimmed, "+") {
		trimmed = strings.TrimPrefix(trimmed, "+")
	}
	if strings.HasPrefix(trimmed, "-") {
		return 0, fmt.Errorf("amount must be positive")
	}
	if strings.HasPrefix(trimmed, ".") {
		trimmed = "0" + trimmed
	}
	parts := strings.SplitN(trimmed, ".", 2)
	majorPart := parts[0]
	if majorPart == "" {
		majorPart = "0"
	}
	major, err := strconv.ParseInt(majorPart, 10, 64)
	if err != nil {
		return 0, err
	}
	if major < 0 {
		return 0, fmt.Errorf("amount must be positive")
	}
	var minor int64
	if len(parts) == 2 {
		frac := parts[1]
		if frac == "" {
			frac = "0"
		}
		if len(frac) > 2 {
			frac = frac[:2]
		}
		for len(frac) < 2 {
			frac += "0"
		}
		parsedMinor, err := strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, err
		}
		minor = parsedMinor
	}
	return major*100 + minor, nil
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
			tgbotapi.NewInlineKeyboardButtonData("🎯 Мой тариф", "plan_info"),
			tgbotapi.NewInlineKeyboardButtonData("🎁 Рефералы", "referral_info"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💳 Баланс", "billing_balance"),
			tgbotapi.NewInlineKeyboardButtonData("💰 Пополнить", "billing_topup"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🛒 Подписка", "billing_subscribe"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("ℹ️ Помощь", "help_menu"),
		),
	)
	return &buttons
}

func (h *Handler) mainPlanLines(plan domain.UserPlan) (string, string) {
	channel := "Каналы: без ограничений."
	if plan.ChannelLimit > 0 {
		channel = fmt.Sprintf("Каналы: до %d сохранённых.", plan.ChannelLimit)
	}
	manual := "Ручные дайджесты: без ограничений."
	switch {
	case plan.ManualDailyLimit <= 0:
		manual = "Ручные дайджесты: без ограничений."
	case plan.ManualIntroTotal > 0:
		manual = fmt.Sprintf("Ручные дайджесты: %d мгновенно, затем до %d в день.", plan.ManualIntroTotal, plan.ManualDailyLimit)
	default:
		manual = fmt.Sprintf("Ручные дайджесты: до %d в день.", plan.ManualDailyLimit)
	}
	return channel, manual
}

func (h *Handler) buildStartSections(user domain.User) []string {
	plan := user.Plan()
	channelLine, manualLine := h.mainPlanLines(plan)

	intro := []string{
		"👋 Добро пожаловать в TG Digest Bot!",
		"",
		fmt.Sprintf("Ваш текущий тариф: %s.", plan.Name),
		"",
		"Основные лимиты:",
		fmt.Sprintf("• %s", channelLine),
		fmt.Sprintf("• %s", manualLine),
		"",
		"Используйте кнопки под сообщением, чтобы сразу перейти к нужному действию.",
	}

	quickStart := []string{
		"🚀 Быстрый старт:",
		"• ➕ Добавьте канал через кнопку «Добавить канал» или команду /add @alias.",
		"• 🏷 Назначьте теги командой /tag @alias тема1, тема2, чтобы группировать каналы.",
		"• 📰 Получите дайджест за 24 часа кнопкой «Дайджест» или командой /digest_now.",
		"• 📌 Попробуйте тематический дайджест через «Дайджест по тегам» или /digest_tag новости.",
		"• 🗓 Настройте автоматическую рассылку кнопкой «Расписание» или /schedule 21:30.",
	}

	var sections []string
	sections = append(sections, strings.Join(intro, "\n"), strings.Join(quickStart, "\n"))

	if referral := h.buildReferralPreview(user); referral != "" {
		sections = append(sections, referral)
	}

	return sections
}

func (h *Handler) buildReferralPreview(user domain.User) string {
	code := strings.TrimSpace(user.ReferralCode)
	if code == "" {
		return ""
	}
	link := h.referralLink(user)
	lines := []string{
		"🎁 Реферальная программа:",
		"• Пригласите 3 друзей — тариф Plus, 5 — Pro.",
		fmt.Sprintf("• Уже приглашено: %d.", user.ReferralsCount),
	}
	if link != "" {
		lines = append(lines, fmt.Sprintf("• Ваша ссылка: %s", link))
	}
	lines = append(lines, "• Откройте раздел «🎁 Рефералы», чтобы узнать подробности.")
	return strings.Join(lines, "\n")
}

func (h *Handler) referralLink(user domain.User) string {
	code := strings.TrimSpace(user.ReferralCode)
	if code == "" {
		return ""
	}
	username := strings.TrimSpace(h.bot.Self.UserName)
	if username == "" {
		return ""
	}
	return fmt.Sprintf("https://t.me/%s?start=%s", username, url.QueryEscape(code))
}

func (h *Handler) buildPlanInfoMessage(user domain.User) string {
	plan := user.Plan()
	channelLine, manualLine := h.mainPlanLines(plan)
	lines := []string{
		fmt.Sprintf("🎯 Ваш тариф: %s", plan.Name),
		"",
		"Текущие лимиты:",
		fmt.Sprintf("• %s", channelLine),
		fmt.Sprintf("• %s", manualLine),
	}
	if plan.ManualIntroTotal > 0 {
		lines = append(lines,
			"",
			"Первые мгновенные запросы расходуются автоматически при использовании /digest_now.",
		)
	}
	lines = append(lines,
		"",
		"Нажмите «🎁 Рефералы», чтобы увеличить лимиты приглашениями.",
	)
	lines = append(lines,
		"",
		"Финансы:",
		"• /balance — посмотреть баланс счёта.",
		"• /deposit 500 — пополнить баланс на 500 ₽ через СБП.",
		"• /buy plus — купить подписку Plus, /buy pro — Pro.",
	)
	return strings.Join(lines, "\n")
}

func (h *Handler) buildReferralInfoMessage(user domain.User) string {
	plusTarget, proTarget := domain.ReferralProgressTargets()
	link := h.referralLink(user)
	lines := []string{
		"🎁 Реферальная программа",
		"",
		fmt.Sprintf("Приглашено друзей: %d.", user.ReferralsCount),
		fmt.Sprintf("• %d приглашений — тариф Plus.", plusTarget),
		fmt.Sprintf("• %d приглашений — тариф Pro.", proTarget),
	}
	switch {
	case user.ReferralsCount < plusTarget:
		remaining := plusTarget - user.ReferralsCount
		lines = append(lines, "", fmt.Sprintf("До тарифа Plus осталось пригласить %d.", remaining))
	case user.ReferralsCount < proTarget:
		remaining := proTarget - user.ReferralsCount
		lines = append(lines, "", fmt.Sprintf("До тарифа Pro осталось пригласить %d.", remaining))
	default:
		lines = append(lines, "", "Вы уже достигли максимального тарифа по рефералам. Спасибо, что делитесь ботом!")
	}
	if link != "" {
		lines = append(lines, "", fmt.Sprintf("Поделитесь ссылкой: %s", link))
	}
	lines = append(lines,
		"",
		"Ссылка учитывает только новых пользователей и не засчитывается при переходе самим собой.",
	)
	return strings.Join(lines, "\n")
}

func (h *Handler) referralKeyboard(user domain.User) *tgbotapi.InlineKeyboardMarkup {
	link := h.referralLink(user)
	if link == "" {
		return nil
	}
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("🔗 Открыть ссылку", link),
		),
	)
	return &markup
}

func (h *Handler) sendPlanInfo(chatID, tgUserID int64) {
	user, err := h.users.GetByTGID(tgUserID)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось получить профиль: %v", err), nil)
		return
	}
	h.reply(chatID, h.buildPlanInfoMessage(user), nil)
}

func (h *Handler) sendReferralInfo(chatID, tgUserID int64) {
	user, err := h.users.GetByTGID(tgUserID)
	if err != nil {
		h.reply(chatID, fmt.Sprintf("Не удалось получить профиль: %v", err), nil)
		return
	}
	h.reply(chatID, h.buildReferralInfoMessage(user), h.referralKeyboard(user))
}

func (h *Handler) notifyPlanUpgrade(user domain.User, previousRole domain.UserRole) {
	plan := user.Plan()
	prevPlan := domain.PlanForRole(previousRole)
	channelLine, manualLine := h.mainPlanLines(plan)
	lines := []string{
		"🎉 Ваш тариф обновлён!",
		fmt.Sprintf("Вы перешли с %s на %s благодаря %d приглашённым друзьям.", prevPlan.Name, plan.Name, user.ReferralsCount),
		"",
		"Новые лимиты:",
		fmt.Sprintf("• %s", channelLine),
		fmt.Sprintf("• %s", manualLine),
		"",
		"Спасибо, что делитесь ботом!",
	}
	h.reply(user.TGUserID, strings.Join(lines, "\n"), nil)
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
		"Биллинг:",
		"• /balance — показать баланс счёта.",
		"• /deposit 500 — создать счёт на пополнение через СБП.",
		"• /buy plus — купить подписку Plus (аналогично /buy pro).",
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

type subscriptionOffer struct {
	Key        string
	Role       domain.UserRole
	Title      string
	PriceMinor int64
	Duration   string
	Bullets    []string
}

var defaultTopUpPresets = []int64{30000, 50000, 100000}

func defaultSubscriptionOffers() map[string]subscriptionOffer {
	return map[string]subscriptionOffer{
		"plus": {
			Key:        "plus",
			Role:       domain.UserRolePlus,
			Title:      "Plus",
			PriceMinor: 29900,
			Duration:   "1 месяц",
			Bullets: []string{
				"До 10 каналов",
				"До 3 ручных дайджестов в день",
			},
		},
		"pro": {
			Key:        "pro",
			Role:       domain.UserRolePro,
			Title:      "Pro",
			PriceMinor: 49900,
			Duration:   "1 месяц",
			Bullets: []string{
				"До 15 каналов",
				"До 6 ручных дайджестов в день",
			},
		},
	}
}
