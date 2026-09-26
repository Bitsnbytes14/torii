package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/Bitsnbytes14/torii/internal/redistest"
)

func TestRateLimiter_FixedWindow(t *testing.T) {
	limiter := NewRateLimiter(redistest.New(t))
	ctx := context.Background()
	apiKey := "ratelimit-test-" + time.Now().Format(time.RFC3339Nano)

	// 20s into a minute, so the current window has 40s left.
	clock := time.Date(2026, 1, 1, 12, 0, 20, 0, time.UTC)
	limiter.now = func() time.Time { return clock }

	for i := 1; i <= 3; i++ {
		d, err := limiter.Allow(ctx, apiKey, 3)
		if err != nil {
			t.Fatal(err)
		}
		if !d.Allowed || d.Remaining != 3-i {
			t.Fatalf("request %d: %+v, want allowed with %d remaining", i, d, 3-i)
		}
	}

	d, err := limiter.Allow(ctx, apiKey, 3)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed {
		t.Fatalf("4th request allowed, want denied")
	}
	if d.RetryAfter != 40*time.Second {
		t.Fatalf("RetryAfter = %v, want 40s (time left in the window)", d.RetryAfter)
	}

	// A new window starts from zero regardless of how far over the last one was.
	clock = clock.Add(time.Minute)
	d, err = limiter.Allow(ctx, apiKey, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allowed || d.Remaining != 2 {
		t.Fatalf("first request of next window: %+v", d)
	}
}
