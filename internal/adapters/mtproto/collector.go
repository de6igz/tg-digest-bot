package mtproto

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
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
func NewCollector(apiID int, apiHash string, storage session.Storage, log zerolog.Logger) (*Collector, error) {
	client := telegram.NewClient(apiID, apiHash, telegram.Options{SessionStorage: storage})
	return &Collector{client: client, log: log}, nil
}

// Client возвращает MTProto клиент.
func (c *Collector) Client() *telegram.Client {
	return c.client
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
	client  *telegram.Client
	log     zerolog.Logger
	timeout time.Duration
}

// NewResolver создаёт резолвер с MTProto клиентом.
func NewResolver(client *telegram.Client, log zerolog.Logger) *Resolver {
	if client == nil {
		return &Resolver{log: log, timeout: 20 * time.Second}
	}
	return &Resolver{client: client, log: log, timeout: 20 * time.Second}
}

// ResolvePublic возвращает ChannelMeta.
func (r *Resolver) ResolvePublic(alias string) (domain.ChannelMeta, error) {
	username, err := normalizeAlias(alias)
	if err != nil {
		return domain.ChannelMeta{}, err
	}

	if r.client == nil {
		return domain.ChannelMeta{}, fmt.Errorf("MTProto клиент не инициализирован")
	}

	var meta domain.ChannelMeta
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	err = r.client.Run(ctx, func(ctx context.Context) error {
		api := r.client.API()
		resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
		if err != nil {
			return fmt.Errorf("не удалось получить канал %s: %w", username, err)
		}
		for _, chat := range resolved.Chats {
			channel, ok := chat.(*tg.Channel)
			if !ok {
				continue
			}
			if channel.Username == "" {
				return fmt.Errorf("канал %s приватный", username)
			}
			meta = domain.ChannelMeta{
				ID:     channel.ID,
				Alias:  strings.ToLower(channel.Username),
				Title:  channel.Title,
				Public: true,
			}
			return nil
		}
		return fmt.Errorf("канал %s не найден", username)
	})
	if err != nil {
		r.log.Error().Err(err).Str("alias", username).Msg("ошибка резолва канала")
		return domain.ChannelMeta{}, err
	}
	if meta.Title == "" {
		return domain.ChannelMeta{}, fmt.Errorf("канал %s не найден", username)
	}
	r.log.Debug().Str("alias", meta.Alias).Str("title", meta.Title).Msg("канал найден")
	return meta, nil
}

func normalizeAlias(alias string) (string, error) {
	trimmed := strings.TrimSpace(alias)
	trimmed = strings.TrimPrefix(trimmed, "https://")
	trimmed = strings.TrimPrefix(trimmed, "http://")
	trimmed = strings.TrimPrefix(trimmed, "t.me/")
	trimmed = strings.TrimPrefix(trimmed, "@")
	if trimmed == "" {
		return "", fmt.Errorf("пустой alias")
	}
	if strings.Contains(trimmed, "/") {
		parts := strings.Split(trimmed, "/")
		if len(parts) == 0 || parts[0] == "" {
			return "", fmt.Errorf("некорректный alias")
		}
		trimmed = parts[0]
	}
	return trimmed, nil
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

var _ session.Storage = (*SessionInMemory)(nil)
