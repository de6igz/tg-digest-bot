package domain

import (
	"context"
	"time"
)

// BusinessMetric описывает бизнесовое событие, которое сохраняется для последующего анализа.
type BusinessMetric struct {
	Event      string
	UserID     *int64
	ChannelID  *int64
	Metadata   map[string]any
	OccurredAt time.Time
}

const (
	// BusinessMetricEventUserRegistered фиксирует регистрацию нового пользователя.
	BusinessMetricEventUserRegistered = "user_registered"
	// BusinessMetricEventChannelAttached фиксирует привязку канала к пользователю.
	BusinessMetricEventChannelAttached = "channel_attached"
	// BusinessMetricEventDigestRequested фиксирует постановку дайджеста в очередь.
	BusinessMetricEventDigestRequested = "digest_requested"
	// BusinessMetricEventDigestScheduled фиксирует плановую постановку дайджеста.
	BusinessMetricEventDigestScheduled = "digest_scheduled"
	// BusinessMetricEventDigestBuilt фиксирует сбор дайджеста и сохранение его содержимого.
	BusinessMetricEventDigestBuilt = "digest_built"
	// BusinessMetricEventDigestDelivered фиксирует успешную доставку дайджеста пользователю.
	BusinessMetricEventDigestDelivered = "digest_delivered"
)

// BusinessMetricRepo сохраняет бизнесовые события.
type BusinessMetricRepo interface {
	RecordBusinessMetric(ctx context.Context, metric BusinessMetric) error
}
