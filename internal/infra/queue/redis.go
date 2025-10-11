package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"tg-digest-bot/internal/domain"
)

// RedisDigestQueue реализует очередь задач на базе Redis lists.
type RedisDigestQueue struct {
	client *redis.Client
	key    string
}

// NewRedisDigestQueue создаёт очередь по указанному ключу.
func NewRedisDigestQueue(client *redis.Client, key string) *RedisDigestQueue {
	return &RedisDigestQueue{client: client, key: key}
}

// Enqueue публикует задачу в очередь.
func (q *RedisDigestQueue) Enqueue(ctx context.Context, job domain.DigestJob) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}
	if err := q.client.LPush(ctx, q.key, payload).Err(); err != nil {
		return fmt.Errorf("push job: %w", err)
	}
	return nil
}

// Pop блокирующе читает задачу из очереди.
func (q *RedisDigestQueue) Pop(ctx context.Context) (domain.DigestJob, error) {
	for {
		if err := ctx.Err(); err != nil {
			return domain.DigestJob{}, err
		}

		res, err := q.client.BRPop(ctx, time.Second, q.key).Result()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				if ctx.Err() != nil {
					return domain.DigestJob{}, ctx.Err()
				}
				continue
			}
			if errors.Is(err, redis.Nil) {
				continue
			}
			return domain.DigestJob{}, err
		}
		if len(res) != 2 {
			return domain.DigestJob{}, errors.New("redis queue: unexpected response")
		}
		var job domain.DigestJob
		if err := json.Unmarshal([]byte(res[1]), &job); err != nil {
			return domain.DigestJob{}, fmt.Errorf("decode job: %w", err)
		}
		return job, nil
	}
}
