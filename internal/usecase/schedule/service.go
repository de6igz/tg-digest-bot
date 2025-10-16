package schedule

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tg-digest-bot/internal/domain"
)

// ErrInvalidTimezone возвращается, если указан некорректный часовой пояс.
var ErrInvalidTimezone = errors.New("invalid timezone")

// Service отвечает за расписание пользователя.
type Service struct {
	users domain.UserRepo
}

// NewService создаёт сервис.
func NewService(users domain.UserRepo) *Service {
	return &Service{users: users}
}

// UpdateDailyTime устанавливает новое время доставки.
func (s *Service) UpdateDailyTime(ctx context.Context, tgUserID int64, local time.Time) error {
	user, err := s.users.GetByTGID(tgUserID)
	if err != nil {
		return fmt.Errorf("получение пользователя: %w", err)
	}
	return s.users.UpdateDailyTime(user.ID, local)
}

// UpdateTimezone сохраняет часовой пояс пользователя.
func (s *Service) UpdateTimezone(ctx context.Context, tgUserID int64, timezone string) error {
	normalized, err := normalizeTimezone(timezone)
	if err != nil {
		return err
	}
	user, err := s.users.GetByTGID(tgUserID)
	if err != nil {
		return fmt.Errorf("получение пользователя: %w", err)
	}
	if err := s.users.UpdateTimezone(user.ID, normalized); err != nil {
		return fmt.Errorf("обновление часового пояса: %w", err)
	}
	return nil
}

func normalizeTimezone(raw string) (string, error) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return "", ErrInvalidTimezone
	}
	candidate = strings.ReplaceAll(candidate, " ", "_")
	if _, err := time.LoadLocation(candidate); err == nil {
		return candidate, nil
	}

	lower := strings.ToLower(candidate)
	parts := strings.Split(lower, "/")
	for i, part := range parts {
		segments := strings.Split(part, "_")
		for j, segment := range segments {
			pieces := strings.Split(segment, "-")
			for k, piece := range pieces {
				if piece == "" {
					continue
				}
				pieces[k] = strings.ToUpper(piece[:1]) + piece[1:]
			}
			segments[j] = strings.Join(pieces, "-")
		}
		parts[i] = strings.Join(segments, "_")
	}
	normalized := strings.Join(parts, "/")
	if _, err := time.LoadLocation(normalized); err == nil {
		return normalized, nil
	}
	return "", ErrInvalidTimezone
}
