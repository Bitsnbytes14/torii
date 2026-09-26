package startup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestWaitFor_SucceedsOnceDependencyIsUp(t *testing.T) {
	calls := 0
	err := WaitFor(context.Background(), discard, "db", func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("connection refused")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil after dependency came up", err)
	}
	if calls != 3 {
		t.Fatalf("check called %d times, want 3", calls)
	}
}

func TestWaitFor_GivesUpWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	cause := errors.New("connection refused")
	start := time.Now()
	err := WaitFor(ctx, discard, "redis", func(context.Context) error { return cause })

	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want it to wrap the last check error", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("returned after %v, want promptly after the 600ms deadline", elapsed)
	}
}
