package tg

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// AuthPasswordResult заглушка.
type AuthPasswordResult struct{}

// ContactsResolveUsernameRequest описывает запрос.
type ContactsResolveUsernameRequest struct {
	Username string
}

// ChatClass базовый интерфейс чатов.
type ChatClass interface {
	chat()
}

// Channel представляет публичный канал.
type Channel struct {
	ID         int64
	AccessHash int64
	Title      string
	Username   string
}

func (c *Channel) chat() {}

// ContactsResolvedPeer результат резолва.
type ContactsResolvedPeer struct {
	Chats []ChatClass
}

// Client упрощённый tg клиент.
type Client struct {
	resolver func(ctx context.Context, username string) (*ContactsResolvedPeer, error)
}

// NewClient создаёт клиента.
func NewClient(resolver func(ctx context.Context, username string) (*ContactsResolvedPeer, error)) *Client {
	return &Client{resolver: resolver}
}

// ContactsResolveUsername эмулирует вызов MTProto.
func (c *Client) ContactsResolveUsername(ctx context.Context, req *ContactsResolveUsernameRequest) (*ContactsResolvedPeer, error) {
	if req == nil || strings.TrimSpace(req.Username) == "" {
		return nil, fmt.Errorf("username пуст")
	}
	if c.resolver != nil {
		return c.resolver(ctx, req.Username)
	}
	alias := strings.ToLower(strings.TrimSpace(req.Username))
	title := DefaultTitle(alias)
	return &ContactsResolvedPeer{Chats: []ChatClass{&Channel{ID: 1, AccessHash: 1, Title: title, Username: alias}}}, nil
}

// DefaultTitle формирует простое название канала.
func DefaultTitle(alias string) string {
	alias = strings.ReplaceAll(alias, "_", " ")
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return "Канал"
	}
	r, size := utf8.DecodeRuneInString(alias)
	if r == utf8.RuneError {
		return strings.ToUpper(alias)
	}
	upper := string(unicode.ToTitle(r))
	return upper + alias[size:]
}
