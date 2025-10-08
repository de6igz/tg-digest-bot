package telegram

import (
	"context"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

type Client struct {
	resolver func(ctx context.Context, username string) (*tg.ContactsResolvedPeer, error)
}

type Options struct {
	SessionStorage auth.SessionStorage
}

func NewClient(apiID int, apiHash string, options Options) *Client {
	_ = apiID
	_ = apiHash
	_ = options
	return &Client{}
}

func (c *Client) Run(ctx context.Context, f func(context.Context) error) error {
	return f(ctx)
}

// API возвращает упрощённый клиент tg.
func (c *Client) API() *tg.Client {
	return tg.NewClient(func(ctx context.Context, username string) (*tg.ContactsResolvedPeer, error) {
		if c.resolver != nil {
			return c.resolver(ctx, username)
		}
		title := tg.DefaultTitle(username)
		return &tg.ContactsResolvedPeer{Chats: []tg.ChatClass{&tg.Channel{ID: 1, AccessHash: 1, Title: title, Username: username}}}, nil
	})
}

// WithResolver позволяет тестам задавать заглушку.
func (c *Client) WithResolver(fn func(ctx context.Context, username string) (*tg.ContactsResolvedPeer, error)) {
	c.resolver = fn
}
