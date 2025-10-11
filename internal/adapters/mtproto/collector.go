package mtproto

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	alias := channel.Alias
	if alias == "" {
		return nil, fmt.Errorf("channel alias is empty")
	}
	normalized, err := normalizeAlias(alias)
	if err != nil {
		return nil, err
	}

	since := time.Now().UTC().Add(-24 * time.Hour)
	posts := make([]domain.Post, 0, 64)

	runErr := c.client.Run(context.Background(), func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()

		api := c.client.API()
		resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: normalized})
		if err != nil {
			return fmt.Errorf("resolve channel %s: %w", normalized, err)
		}

		var resolvedChannel *tg.Channel
		for _, chat := range resolved.Chats {
			ch, ok := chat.(*tg.Channel)
			if !ok {
				continue
			}
			if channel.TGChannelID != 0 && ch.ID == channel.TGChannelID {
				resolvedChannel = ch
				break
			}
			if strings.EqualFold(ch.Username, normalized) {
				resolvedChannel = ch
				break
			}
		}
		if resolvedChannel == nil {
			return fmt.Errorf("канал %s не найден", normalized)
		}

		peer := &tg.InputPeerChannel{ChannelID: resolvedChannel.ID, AccessHash: resolvedChannel.AccessHash}
		limit := 100
		maxID := 0

		for {
			req := &tg.MessagesGetHistoryRequest{
				Peer:  peer,
				Limit: limit,
			}
			if maxID > 0 {
				req.MaxID = maxID
			}

			history, err := api.MessagesGetHistory(ctx, req)
			if err != nil {
				return fmt.Errorf("messages.getHistory: %w", err)
			}

			channelMessages, ok := history.(*tg.MessagesChannelMessages)
			if !ok {
				return fmt.Errorf("unexpected history response %T", history)
			}
			if len(channelMessages.Messages) == 0 {
				break
			}

			oldestID := 0
			stop := false

			for _, msg := range channelMessages.Messages {
				tm, ok := msg.(*tg.Message)
				if !ok {
					continue
				}
				if oldestID == 0 || tm.ID < oldestID {
					oldestID = tm.ID
				}

				published := time.Unix(int64(tm.Date), 0).UTC()
				if published.Before(since) {
					stop = true
					continue
				}

				text := strings.TrimSpace(tm.Message)
				if text == "" && tm.Media == nil {
					continue
				}

				meta := buildMessageMeta(tm)
				rawMeta, err := json.Marshal(meta)
				if err != nil {
					c.log.Error().Err(err).Msg("collector: не удалось сериализовать метаданные сообщения")
					rawMeta = nil
				}

				posts = append(posts, domain.Post{
					ChannelID:   channel.ID,
					TGMsgID:     int64(tm.ID),
					PublishedAt: published,
					URL:         fmt.Sprintf("https://t.me/%s/%d", normalized, tm.ID),
					Text:        text,
					RawMetaJSON: rawMeta,
					Hash:        hashMessage(channel.ID, tm.ID, text),
				})
			}

			if stop || len(channelMessages.Messages) < limit {
				break
			}
			if oldestID <= 1 {
				break
			}
			maxID = oldestID - 1
		}
		return nil
	})
	if runErr != nil {
		return nil, runErr
	}

	sort.Slice(posts, func(i, j int) bool {
		return posts[i].PublishedAt.After(posts[j].PublishedAt)
	})

	return posts, nil
}

type messageMeta struct {
	Views      int        `json:"views,omitempty"`
	Forwards   int        `json:"forwards,omitempty"`
	Replies    int        `json:"replies,omitempty"`
	Reactions  int        `json:"reactions,omitempty"`
	PostAuthor string     `json:"post_author,omitempty"`
	EditDate   *time.Time `json:"edit_date,omitempty"`
	HasMedia   bool       `json:"has_media,omitempty"`
	Entities   int        `json:"entities,omitempty"`
}

func buildMessageMeta(msg *tg.Message) messageMeta {
	meta := messageMeta{}
	if msg == nil {
		return meta
	}
	if views, ok := msg.GetViews(); ok {
		meta.Views = views
	}
	if forwards, ok := msg.GetForwards(); ok {
		meta.Forwards = forwards
	}
	if replies, ok := msg.GetReplies(); ok {
		meta.Replies = replies.Replies
	}
	if reactions, ok := msg.GetReactions(); ok {
		for _, reaction := range reactions.Results {
			meta.Reactions += reaction.Count
		}
	}
	if editDate, ok := msg.GetEditDate(); ok {
		t := time.Unix(int64(editDate), 0).UTC()
		meta.EditDate = &t
	}
	if author, ok := msg.GetPostAuthor(); ok && author != "" {
		meta.PostAuthor = author
	}
	if len(msg.Entities) > 0 {
		meta.Entities = len(msg.Entities)
	}
	meta.HasMedia = msg.Media != nil
	return meta
}

func hashMessage(channelID int64, messageID int, text string) string {
	data := fmt.Sprintf("%d:%d:%s", channelID, messageID, text)
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
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

// SessionInMemory хранит сессию в памяти (используется в тестах).
type SessionInMemory struct {
	mu   sync.RWMutex
	data []byte
}

// LoadSession загружает сессию.
func (s *SessionInMemory) LoadSession(ctx context.Context) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.data) == 0 {
		return nil, session.ErrNotFound
	}
	clone := make([]byte, len(s.data))
	copy(clone, s.data)
	return clone, nil
}

// StoreSession сохраняет сессию.
func (s *SessionInMemory) StoreSession(ctx context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = append(s.data[:0], data...)
	return nil
}

// SessionFile хранит MTProto-сессию в файловой системе.
type SessionFile struct {
	path string
	mu   sync.RWMutex
}

// NewSessionFile создаёт файловое хранилище MTProto-сессии.
func NewSessionFile(path string) *SessionFile {
	return &SessionFile{path: path}
}

// LoadSession читает сессию из файла.
func (s *SessionFile) LoadSession(ctx context.Context) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.path == "" {
		return nil, session.ErrNotFound
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, session.ErrNotFound
		}
		return nil, fmt.Errorf("чтение MTProto-сессии: %w", err)
	}
	if len(data) == 0 {
		return nil, session.ErrNotFound
	}
	clone := make([]byte, len(data))
	copy(clone, data)
	return clone, nil
}

// StoreSession пишет сессию в файл с правами 0600.
func (s *SessionFile) StoreSession(ctx context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.path == "" {
		return fmt.Errorf("путь к MTProto-сессии не задан")
	}

	dir := filepath.Dir(s.path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("создание каталога MTProto-сессии: %w", err)
		}
	}

	tmp := make([]byte, len(data))
	copy(tmp, data)
	if err := os.WriteFile(s.path, tmp, 0o600); err != nil {
		return fmt.Errorf("запись MTProto-сессии: %w", err)
	}
	return nil
}

var (
	_ session.Storage = (*SessionInMemory)(nil)
	_ session.Storage = (*SessionFile)(nil)
)
