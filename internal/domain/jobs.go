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
	UserTGID    int64          `json:"user_tg_id"`
	ChatID      int64          `json:"chat_id"`
	ChannelID   int64          `json:"channel_id,omitempty"`
	Date        time.Time      `json:"date"`
	RequestedAt time.Time      `json:"requested_at"`
	Cause       DigestJobCause `json:"cause"`
}

// DigestQueue описывает очередь задач на построение дайджестов.
type DigestQueue interface {
	Enqueue(ctx context.Context, job DigestJob) error
	Pop(ctx context.Context) (DigestJob, error)
}
