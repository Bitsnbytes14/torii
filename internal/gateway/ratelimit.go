package gateway

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const rateLimitWindow = time.Minute

type RateLimitDecision struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
}

// RateLimiter is a fixed-window counter kept in Redis rather than in process
// memory. That is what makes the limit a property of the tenant instead of
// the replica: every gateway INCRs the same key, and INCR is atomic, so two
// replicas can never both see "4" and both let a fifth request through.
//
// Fixed windows are the simplest correct choice, with one known weakness: a
// tenant can send `limit` requests at 0:59 and another `limit` at 1:00, so
// up to 2x the limit can land within a short span straddling a boundary.
// Sliding-window or token-bucket algorithms smooth that out at the cost of
// more state per tenant.
type RateLimiter struct {
	rdb *redis.Client
	now func() time.Time
}

func NewRateLimiter(rdb *redis.Client) *RateLimiter {
	return &RateLimiter{rdb: rdb, now: time.Now}
}

func (l *RateLimiter) Allow(ctx context.Context, tenantID string, limit int) (RateLimitDecision, error) {
	now := l.now()
	window := now.Unix() / int64(rateLimitWindow.Seconds())
	key := fmt.Sprintf("tenant:%s:ratelimit:%d", tenantID, window)

	// INCR and EXPIRE go in one MULTI so a crash between them can't leave a
	// counter with no TTL. The TTL is two windows, not one, because replica
	// clocks can disagree slightly: a replica running a few seconds behind may
	// still be INCRing the previous window's key after it would have expired.
	var incr *redis.IntCmd
	_, err := l.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		incr = p.Incr(ctx, key)
		p.Expire(ctx, key, 2*rateLimitWindow)
		return nil
	})
	if err != nil {
		return RateLimitDecision{}, fmt.Errorf("rate limit counter: %w", err)
	}

	count := int(incr.Val())
	windowEnd := time.Unix((window+1)*int64(rateLimitWindow.Seconds()), 0)
	return RateLimitDecision{
		Allowed:    count <= limit,
		Limit:      limit,
		Remaining:  max(0, limit-count),
		RetryAfter: windowEnd.Sub(now),
	}, nil
}
