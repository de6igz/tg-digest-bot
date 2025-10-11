package main

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"tg-digest-bot/internal/adapters/repo"
	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/config"
	"tg-digest-bot/internal/infra/db"
	"tg-digest-bot/internal/infra/queue"
)

func main() {
	cfg := config.Load()
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
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		log.Fatal().Err(err).Msg("scheduler: не удалось подключиться к Redis")
	}
	digestQueue := queue.NewRedisDigestQueue(redisClient, cfg.Queues.Digest)

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		users, err := repoAdapter.ListForDailyTime(time.Now().UTC())
		if err != nil {
			log.Error().Err(err).Msg("scheduler: ошибка выборки пользователей")
			continue
		}
		for _, user := range users {
			job := domain.DigestJob{
				UserTGID:    user.TGUserID,
				ChatID:      user.TGUserID,
				Date:        time.Now().UTC(),
				RequestedAt: time.Now().UTC(),
				Cause:       domain.DigestCauseScheduled,
			}
			if err := digestQueue.Enqueue(context.Background(), job); err != nil {
				log.Error().Err(err).Int64("user", user.TGUserID).Msg("scheduler: не удалось поставить задачу дайджеста")
			}
		}
	}
}
