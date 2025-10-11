package channels

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"tg-digest-bot/internal/domain"
)

var (
	ErrChannelLimit   = errors.New("превышен лимит каналов")
	ErrPrivateChannel = errors.New("канал приватный или недоступен")
	ErrAliasInvalid   = errors.New("некорректный алиас")
)

var aliasRegex = regexp.MustCompile(`(?i)^(?:@|https?://t\.me/|t\.me/)?([a-z0-9_]{5,})$`)

// Service управляет каналами пользователя.
type Service struct {
	repo     domain.ChannelRepo
	resolver domain.ChannelResolver
	userRepo domain.UserRepo
	limit    int
}

// NewService создаёт новый сервис каналов.
func NewService(repo domain.ChannelRepo, resolver domain.ChannelResolver, userRepo domain.UserRepo, limit int) *Service {
	return &Service{repo: repo, resolver: resolver, userRepo: userRepo, limit: limit}
}

// ParseAlias приводит ввод пользователя к каноничному алиасу.
func ParseAlias(input string) (string, error) {
	trim := strings.TrimSpace(input)
	matches := aliasRegex.FindStringSubmatch(trim)
	if len(matches) < 2 {
		return "", ErrAliasInvalid
	}
	return strings.ToLower(matches[1]), nil
}

// AddChannel добавляет канал пользователю.
func (s *Service) AddChannel(ctx context.Context, tgUserID int64, alias string) (domain.Channel, error) {
	parsed, err := ParseAlias(alias)
	if err != nil {
		return domain.Channel{}, err
	}
	user, err := s.userRepo.GetByTGID(tgUserID)
	if err != nil {
		return domain.Channel{}, fmt.Errorf("получение пользователя: %w", err)
	}
	count, err := s.repo.CountUserChannels(user.ID)
	if err != nil {
		return domain.Channel{}, fmt.Errorf("подсчёт каналов: %w", err)
	}
	if s.limit > 0 && count >= s.limit {
		return domain.Channel{}, ErrChannelLimit
	}
	meta, err := s.resolver.ResolvePublic(parsed)
	if err != nil {
		return domain.Channel{}, fmt.Errorf("резолв канала: %w", err)
	}
	if !meta.Public {
		return domain.Channel{}, ErrPrivateChannel
	}
	channel, err := s.repo.UpsertChannel(meta)
	if err != nil {
		return domain.Channel{}, fmt.Errorf("сохранение канала: %w", err)
	}
	if err := s.repo.AttachChannelToUser(user.ID, channel.ID); err != nil {
		return domain.Channel{}, fmt.Errorf("привязка канала: %w", err)
	}
	return channel, nil
}

// ListChannels возвращает каналы пользователя.
func (s *Service) ListChannels(ctx context.Context, tgUserID int64, limit, offset int) ([]domain.UserChannel, error) {
	user, err := s.userRepo.GetByTGID(tgUserID)
	if err != nil {
		return nil, fmt.Errorf("получение пользователя: %w", err)
	}
	return s.repo.ListUserChannels(user.ID, limit, offset)
}

// ToggleMute переключает статус мутирования канала.
func (s *Service) ToggleMute(ctx context.Context, tgUserID, channelID int64, mute bool) error {
	user, err := s.userRepo.GetByTGID(tgUserID)
	if err != nil {
		return fmt.Errorf("получение пользователя: %w", err)
	}
	return s.repo.SetMuted(user.ID, channelID, mute)
}

// RemoveChannel отвязывает канал от пользователя.
func (s *Service) RemoveChannel(ctx context.Context, tgUserID, channelID int64) error {
	user, err := s.userRepo.GetByTGID(tgUserID)
	if err != nil {
		return fmt.Errorf("получение пользователя: %w", err)
	}
	return s.repo.DetachChannelFromUser(user.ID, channelID)
}

// UpdateChannelTags обновляет список тегов для канала пользователя.
func (s *Service) UpdateChannelTags(ctx context.Context, tgUserID, channelID int64, tags []string) error {
	user, err := s.userRepo.GetByTGID(tgUserID)
	if err != nil {
		return fmt.Errorf("получение пользователя: %w", err)
	}
	cleaned := NormalizeTags(tags)
	channels, err := s.repo.ListUserChannels(user.ID, 100, 0)
	if err != nil {
		return fmt.Errorf("получение каналов: %w", err)
	}
	var found bool
	for _, ch := range channels {
		if ch.ChannelID == channelID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("канал не найден среди подписок пользователя")
	}
	return s.repo.UpdateUserChannelTags(user.ID, channelID, cleaned)
}

// NormalizeTags удаляет пустые и дублирующиеся значения, сохраняя порядок.
func NormalizeTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	cleaned := make([]string, 0, len(tags))
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, trimmed)
	}
	return cleaned
}
