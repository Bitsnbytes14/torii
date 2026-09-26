package gateway

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Bitsnbytes14/torii/internal/tenant"
)

type TenantLookup interface {
	GetByAPIKeyHash(ctx context.Context, hash string) (*tenant.Tenant, error)
}

type cacheEntry struct {
	tenant    *tenant.Tenant
	expiresAt time.Time
}

// TenantCache keeps a per-replica, TTL-bounded copy of tenant config. The TTL
// is the hot-reload mechanism: there's no invalidation message from the
// control plane, so a change is visible to every replica within one TTL.
// That trades up to TTL of staleness for not paying a Redis round trip on
// every request just to re-read config that almost never changes.
type TenantCache struct {
	source TenantLookup
	ttl    time.Duration
	logger *slog.Logger
	now    func() time.Time

	mu      sync.RWMutex
	entries map[string]cacheEntry
	// Collapses concurrent refreshes of the same key into one Redis read, so
	// an entry expiring under load doesn't turn into a burst of identical GETs.
	group singleflight.Group
}

func NewTenantCache(source TenantLookup, ttl time.Duration, logger *slog.Logger) *TenantCache {
	return &TenantCache{
		source:  source,
		ttl:     ttl,
		logger:  logger,
		now:     time.Now,
		entries: make(map[string]cacheEntry),
	}
}

// Get returns tenant.ErrNotFound for unknown keys. The cache is keyed by the
// key's SHA-256 hash, not the plaintext: that way a heap dump or a stray log
// of the cache's internal state can't recover a working credential, only the
// same hash Redis already stores. Unknown keys are not cached: every entry
// would be attacker-chosen, so a flood of random keys would grow the map
// without bound. Redis GETs are cheap enough to absorb it.
func (c *TenantCache) Get(ctx context.Context, apiKey string) (*tenant.Tenant, error) {
	hash := tenant.HashAPIKey(apiKey)

	c.mu.RLock()
	entry, ok := c.entries[hash]
	c.mu.RUnlock()
	if ok && c.now().Before(entry.expiresAt) {
		return entry.tenant, nil
	}

	v, err, _ := c.group.Do(hash, func() (any, error) {
		// Detached from the caller: the result is shared with every waiter,
		// so one client disconnecting shouldn't fail the lookup for the rest.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		return c.source.GetByAPIKeyHash(fetchCtx, hash)
	})

	switch {
	case err == nil:
		t := v.(*tenant.Tenant)
		c.mu.Lock()
		c.entries[hash] = cacheEntry{tenant: t, expiresAt: c.now().Add(c.ttl)}
		c.mu.Unlock()
		return t, nil

	case errors.Is(err, tenant.ErrNotFound):
		// Evict so a deleted tenant stops working within one TTL.
		c.mu.Lock()
		delete(c.entries, hash)
		c.mu.Unlock()
		return nil, tenant.ErrNotFound

	case ok:
		// Redis is unreachable but this tenant was valid a moment ago. Serving
		// the stale entry keeps known tenants working through a Redis blip;
		// the entry stays expired, so the next request retries the refresh.
		c.logger.Warn("tenant refresh failed, serving stale config", "error", err)
		return entry.tenant, nil

	default:
		return nil, err
	}
}
