// Package redistest gives tests a Redis client. With TEST_REDIS_URL set it
// uses that real server; otherwise it starts an in-process miniredis so
// `go test ./...` still covers the Redis-backed code with nothing running.
package redistest

import (
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// New never flushes the database: tests only touch keys derived from freshly
// generated API keys, so they can share a real Redis with a running dev stack.
func New(t *testing.T) *redis.Client {
	t.Helper()

	if url := os.Getenv("TEST_REDIS_URL"); url != "" {
		opts, err := redis.ParseURL(url)
		if err != nil {
			t.Fatalf("parse TEST_REDIS_URL: %v", err)
		}
		rdb := redis.NewClient(opts)
		t.Cleanup(func() { _ = rdb.Close() })
		return rdb
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}
