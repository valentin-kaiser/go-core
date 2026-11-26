//go:build integration

package cache_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	redisURLEnv     = "REDIS_URL"
	defaultRedisURL = "redis://localhost:6379"
	redisStartHint  = "docker compose -f test/compose.yaml up -d"
	redisShortTTL   = time.Second
	redisExpiryWait = 10 * time.Second
	pingTimeout     = 5 * time.Second

	eventBufferSize       = 10
	tieredEventBufferSize = 20
	tieredL1MaxSize       = 5
	ttlTolerance          = 100 * time.Millisecond
)

// connectRedis returns a client for the server of REDIS_URL and fails the test when none answers; the cache that wraps it closes it.
func connectRedis(tb testing.TB) *redis.Client {
	tb.Helper()

	url := os.Getenv(redisURLEnv)
	if url == "" {
		url = defaultRedisURL
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		tb.Fatalf("parsing %s %q failed: %v", redisURLEnv, url, err)
	}
	client := redis.NewClient(opt)

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	err = client.Ping(ctx).Err()
	if err != nil {
		closeErr := client.Close()
		if closeErr != nil {
			tb.Errorf("closing Redis client failed: %v", closeErr)
		}
		tb.Fatalf("Redis not available at %s, start it with `%s`: %v", url, redisStartHint, err)
	}
	return client
}
