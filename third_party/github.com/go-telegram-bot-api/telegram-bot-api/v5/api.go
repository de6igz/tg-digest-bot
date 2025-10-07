package tgbotapi

import "errors"

type BotAPI struct {
	Token string
}

func NewBotAPI(token string) (*BotAPI, error) {
	if token == "" {
		return nil, errors.New("token required")
	}
	return &BotAPI{Token: token}, nil
}

type Update struct {
	Message       *Message
	CallbackQuery *CallbackQuery
}

type Message struct {
	Chat   Chat
	From   *User
	Text   string
	ChatID int64
}

type Chat struct {
	ID int64
}

type User struct {
	ID int64
}

type MessageConfig struct {
	ChatID      int64
	Text        string
	ReplyMarkup any
}

func NewMessage(chatID int64, text string) MessageConfig {
	return MessageConfig{ChatID: chatID, Text: text}
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton
}

type InlineKeyboardButton struct {
	Text string
	Data string
	URL  string
}

func NewInlineKeyboardMarkup(rows ...[]InlineKeyboardButton) InlineKeyboardMarkup {
	return InlineKeyboardMarkup{InlineKeyboard: rows}
}

func NewInlineKeyboardRow(btns ...InlineKeyboardButton) []InlineKeyboardButton {
	return btns
}

func NewInlineKeyboardButtonData(text, data string) InlineKeyboardButton {
	return InlineKeyboardButton{Text: text, Data: data}
}

func NewInlineKeyboardButtonURL(text, url string) InlineKeyboardButton {
	return InlineKeyboardButton{Text: text, URL: url}
}

func (b *BotAPI) Send(cfg MessageConfig) (Message, error) {
	return Message{Chat: Chat{ID: cfg.ChatID}, Text: cfg.Text}, nil
}

type CallbackQuery struct {
	ID      string
	From    *User
	Message *Message
	Data    string
}

type CallbackConfig struct {
	CallbackQueryID string
	Text            string
	ShowAlert       bool
	URL             string
	CacheTime       int
}

func NewCallback(id, text string) CallbackConfig {
	return CallbackConfig{CallbackQueryID: id, Text: text}
}

func (b *BotAPI) Request(cfg interface{}) (interface{}, error) {
	return nil, nil
}
