package domain

import "time"

// Feedback представляет отзыв пользователя.
type Feedback struct {
	ID        int64
	UserID    int64
	ChatID    int64
	Message   string
	CreatedAt time.Time
}
