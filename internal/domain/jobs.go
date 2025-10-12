package domain

import (
	"context"
	"time"
)

// DigestJobCause описывает источник запроса на дайджест.
type DigestJobCause string

const (
	// DigestCauseManual — пользователь запросил дайджест вручную.
	DigestCauseManual DigestJobCause = "manual"
	// DigestCauseScheduled — дайджест запланирован по расписанию.
	DigestCauseScheduled DigestJobCause = "scheduled"
)

// DigestJob содержит информацию о задаче построения дайджеста.
type DigestJob struct {
	ID          string         `json:"job_id,omitempty"`
	UserTGID    int64          `json:"user_tg_id"`
	ChatID      int64          `json:"chat_id"`
	ChannelID   int64          `json:"channel_id,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Date        time.Time      `json:"date"`
	RequestedAt time.Time      `json:"requested_at"`
	Cause       DigestJobCause `json:"cause"`
}

// DigestQueue описывает очередь задач на построение дайджестов.
type DigestQueue interface {
	Enqueue(ctx context.Context, job DigestJob) error
	Receive(ctx context.Context) (DigestJob, DigestAckFunc, error)
}

// DigestAckFunc подтверждает успешную обработку или запрашивает повтор доставки задачи.
type DigestAckFunc func(success bool) error

// ScheduleTaskRepo отвечает за идемпотентное планирование задач дайджеста.
type ScheduleTaskRepo interface {
	// Acquire помечает выполнение задачи на указанное время и возвращает true,
	// если запись была создана. При конфликте возвращает false без ошибки.
	Acquire(userID int64, scheduledFor time.Time) (bool, error)
}

// DigestJobStatusRepo отвечает за отслеживание статуса доставки задач дайджеста.
type DigestJobStatusRepo interface {
	// EnsureDigestJob регистрирует попытку обработки и возвращает признак успешной доставки
	// и номер текущей попытки.
	EnsureDigestJob(jobID string) (delivered bool, attempt int, err error)
	// MarkDigestJobDelivered помечает задачу как окончательно доставленную.
	MarkDigestJobDelivered(jobID string) error
}
