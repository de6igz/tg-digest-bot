package cache

import (
"context"
"time"

"github.com/redis/go-redis/v9"
)

// RedisCache реализует domain.Cache через Redis.
type RedisCache struct {
client *redis.Client
}

// NewRedis создаёт кэш.
func NewRedis(client *redis.Client) *RedisCache {
return &RedisCache{client: client}
}

// Once выполняет функцию, если ключ ещё не задан.
func (c *RedisCache) Once(key string, ttl time.Duration, fn func() error) error {
ctx := context.Background()
ok, err := c.client.SetNX(ctx, key, "1", ttl).Result()
if err != nil {
return err
}
if !ok {
return nil
}
if err := fn(); err != nil {
_ = c.client.Del(ctx, key).Err()
return err
}
return nil
}

// Set задаёт значение.
func (c *RedisCache) Set(key string, value []byte, ttl time.Duration) error {
return c.client.Set(context.Background(), key, value, ttl).Err()
}

// Get возвращает значение.
func (c *RedisCache) Get(key string) ([]byte, error) {
return c.client.Get(context.Background(), key).Bytes()
}
