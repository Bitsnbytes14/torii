// Package startup holds the dependency wait shared by every service's main.
package startup

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// WaitFor retries check with capped exponential backoff until it succeeds or
// ctx ends. Kubernetes has no depends_on, so pods routinely start before the
// database or Redis they need is ready. Exiting on the first failed ping turns
// that ordinary race into CrashLoopBackOff, whose restart delay grows to
// minutes; waiting in-process keeps first boot fast.
func WaitFor(ctx context.Context, logger *slog.Logger, name string, check func(context.Context) error) error {
	const (
		attemptTimeout = 3 * time.Second
		maxBackoff     = 5 * time.Second
	)
	backoff := 250 * time.Millisecond

	for {
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		err := check(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}

		logger.Warn("dependency not ready, retrying", "dependency", name, "error", err, "retry_in", backoff.String())
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s not ready: %w", name, err)
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}
