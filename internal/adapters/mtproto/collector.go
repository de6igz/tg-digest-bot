package mtproto

import (
"context"
"fmt"
"time"

"github.com/gotd/td/telegram"
"github.com/gotd/td/telegram/auth"
"github.com/gotd/td/tg"
"github.com/rs/zerolog"

"tg-digest-bot/internal/domain"
)

// Collector реализует загрузку сообщений через gotd.
type Collector struct {
client *telegram.Client
log    zerolog.Logger
}

// NewCollector создаёт MTProto клиент на базе токенов.
func NewCollector(apiID int, apiHash string, session auth.SessionStorage, log zerolog.Logger) (*Collector, error) {
client := telegram.NewClient(apiID, apiHash, telegram.Options{SessionStorage: session})
return &Collector{client: client, log: log}, nil
}

// Collect24h собирает историю канала.
func (c *Collector) Collect24h(channel domain.Channel) ([]domain.Post, error) {
ctx := context.Background()
err := c.client.Run(ctx, func(ctx context.Context) error {
// TODO: Реализация сборщика через channels.GetHistory.
return nil
})
if err != nil {
return nil, err
}
c.log.Warn().Str("channel", channel.Alias).Msg("Collect24h заглушка в MVP")
return []domain.Post{{
ChannelID:   channel.ID,
TGMsgID:     time.Now().Unix(),
PublishedAt: time.Now().UTC(),
URL:         fmt.Sprintf("https://t.me/%s/%d", channel.Alias, time.Now().Unix()),
Text:        "Пример сообщения канала",
RawMetaJSON: []byte(`{"type":"stub"}`),
}}, nil
}

// Resolver проверяет публичность каналов через MTProto.
type Resolver struct {
log zerolog.Logger
}

// NewResolver создаёт заглушку резолвера.
func NewResolver(log zerolog.Logger) *Resolver {
return &Resolver{log: log}
}

// ResolvePublic возвращает ChannelMeta.
func (r *Resolver) ResolvePublic(alias string) (domain.ChannelMeta, error) {
r.log.Debug().Str("alias", alias).Msg("ResolvePublic заглушка")
return domain.ChannelMeta{ID: time.Now().Unix(), Alias: alias, Title: "Демо канал", Public: true}, nil
}

// SessionInMemory хранит сессию в памяти (MVP).
type SessionInMemory struct {
data []byte
}

// LoadSession загружает сессию.
func (s *SessionInMemory) LoadSession(ctx context.Context) ([]byte, error) {
return s.data, nil
}

// StoreSession сохраняет сессию.
func (s *SessionInMemory) StoreSession(ctx context.Context, data []byte) error {
s.data = data
return nil
}

var _ auth.SessionStorage = (*SessionInMemory)(nil)

// DummyAuth реализует бот-авторизацию.
type DummyAuth struct{}

// SignIn реализация заглушки.
func (DummyAuth) SignIn(ctx context.Context, client *telegram.Client) error {
return nil
}

// SignUp не используется.
func (DummyAuth) SignUp(ctx context.Context, client *telegram.Client) error {
return nil
}

// Password не используется.
func (DummyAuth) Password(ctx context.Context, client *telegram.Client) (*tg.AuthPasswordResult, error) {
return nil, nil
}
