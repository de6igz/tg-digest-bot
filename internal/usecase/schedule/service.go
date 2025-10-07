package schedule

import (
	"context"
	"fmt"
	"time"

	"tg-digest-bot/internal/domain"
)

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
