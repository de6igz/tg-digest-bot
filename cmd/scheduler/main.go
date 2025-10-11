package main

import (
	"context"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"tg-digest-bot/internal/adapters/repo"
	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/config"
	"tg-digest-bot/internal/infra/db"
	"tg-digest-bot/internal/infra/metrics"
	"tg-digest-bot/internal/infra/queue"
)

func main() {
	cfg := config.Load()

	metrics.MustRegister(prometheus.DefaultRegisterer)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	metrics.StartServer(ctx, log.With().Str("component", "metrics").Logger(), ":9090")
	pool, err := db.Connect(cfg.PGDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("scheduler: нет подключения к БД")
	}
	defer pool.Close()

	repoAdapter := repo.NewPostgres(pool)
	if cfg.RedisAddr == "" {
		log.Fatal().Msg("scheduler: не указан адрес Redis (REDIS_ADDR)")
	}
	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer redisClient.Close()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatal().Err(err).Msg("scheduler: не удалось подключиться к Redis")
	}
	digestQueue := queue.NewRedisDigestQueue(redisClient, cfg.Queues.Digest)

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("scheduler: остановка")
			return
		case <-ticker.C:
			now := time.Now().UTC()
			users, err := repoAdapter.ListForDailyTime(now)
			if err != nil {
				log.Error().Err(err).Msg("scheduler: ошибка выборки пользователей")
				continue
			}
			for _, user := range users {
				scheduledUTC, ok, err := nextScheduledWindow(now, user)
				if err != nil {
					log.Warn().Err(err).Int64("user", user.TGUserID).Msg("scheduler: некорректный часовой пояс, используем UTC")
				}
				if !ok {
					continue
				}
				acquired, err := repoAdapter.AcquireScheduleTask(user.ID, scheduledUTC)
				if err != nil {
					log.Error().Err(err).Int64("user", user.TGUserID).Msg("scheduler: ошибка бронирования задачи")
					continue
				}
				if !acquired {
					continue
				}
				job := domain.DigestJob{
					UserTGID:    user.TGUserID,
					ChatID:      user.TGUserID,
					Date:        scheduledUTC,
					RequestedAt: now,
					Cause:       domain.DigestCauseScheduled,
				}
				if err := digestQueue.Enqueue(ctx, job); err != nil {
					log.Error().Err(err).Int64("user", user.TGUserID).Msg("scheduler: не удалось поставить задачу дайджеста")
				}
			}
		}
	}
}

const scheduleWindow = 10 * time.Minute

func nextScheduledWindow(now time.Time, user domain.User) (time.Time, bool, error) {
	loc := time.UTC
	var loadErr error
	if user.Timezone != "" {
		if l, err := time.LoadLocation(user.Timezone); err == nil {
			loc = l
		} else {
			loadErr = err
		}
	}
	userNow := now.In(loc)
	daily := user.DailyTime.In(time.UTC)
	scheduledLocal := time.Date(userNow.Year(), userNow.Month(), userNow.Day(),
		daily.Hour(), daily.Minute(), daily.Second(), 0, loc)

	if userNow.After(scheduledLocal.Add(scheduleWindow)) {
		scheduledLocal = scheduledLocal.Add(24 * time.Hour)
	}

	diff := scheduledLocal.Sub(userNow)
	if diff < -scheduleWindow || diff > scheduleWindow {
		return time.Time{}, false, loadErr
	}

	return scheduledLocal.UTC(), true, loadErr
}
