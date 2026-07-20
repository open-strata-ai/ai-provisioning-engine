package persistence

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLocker is a Redis-backed domain.Locker using SET NX + EX.
type RedisLocker struct {
	client *redis.Client
}

// NewRedisLocker opens a Redis connection.
func NewRedisLocker(addr string) *RedisLocker {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	return &RedisLocker{client: rdb}
}

const lockPrefix = "lock:"

func (l *RedisLocker) Acquire(key string, ttl time.Duration) bool {
	ok, err := l.client.SetNX(context.Background(), lockPrefix+key, "1", ttl).Result()
	return err == nil && ok
}

func (l *RedisLocker) Release(key string) {
	_ = l.client.Del(context.Background(), lockPrefix+key)
}
