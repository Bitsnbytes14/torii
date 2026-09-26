package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bitsnbytes14/torii/internal/tenant"
)

// fakeLookup is keyed by the same hash TenantCache computes from the raw key
// it's given, so it stands in for the apikey:{hash} -> tenant Redis lookup.
type fakeLookup struct {
	mu      sync.Mutex
	tenants map[string]*tenant.Tenant
	err     error
	calls   atomic.Int32
	delay   time.Duration
}

func (f *fakeLookup) GetByAPIKeyHash(_ context.Context, hash string) (*tenant.Tenant, error) {
	f.calls.Add(1)
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	t, ok := f.tenants[hash]
	if !ok {
		return nil, tenant.ErrNotFound
	}
	copied := *t
	return &copied, nil
}

// set takes the plaintext API key so tests read naturally; it hashes before
// storing, matching what TenantCache.Get will look up with.
func (f *fakeLookup) set(apiKey string, t *tenant.Tenant) {
	f.mu.Lock()
	defer f.mu.Unlock()
	hash := tenant.HashAPIKey(apiKey)
	if t == nil {
		delete(f.tenants, hash)
	} else {
		f.tenants[hash] = t
	}
}

func newTestCache(src TenantLookup, ttl time.Duration) (*TenantCache, *time.Time) {
	c := NewTenantCache(src, ttl, slog.New(slog.NewTextHandler(io.Discard, nil)))
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return clock }
	return c, &clock
}

func TestTenantCache_ServesFromCacheUntilTTL(t *testing.T) {
	src := &fakeLookup{tenants: map[string]*tenant.Tenant{}}
	src.set("k", &tenant.Tenant{ID: "k", RateLimitPerMinute: 5})
	cache, clock := newTestCache(src, 5*time.Second)
	ctx := context.Background()

	got, _ := cache.Get(ctx, "k")
	src.set("k", &tenant.Tenant{ID: "k", RateLimitPerMinute: 50})

	*clock = clock.Add(4 * time.Second)
	got, _ = cache.Get(ctx, "k")
	if got.RateLimitPerMinute != 5 || src.calls.Load() != 1 {
		t.Fatalf("within TTL: limit=%d calls=%d, want cached 5 with 1 call", got.RateLimitPerMinute, src.calls.Load())
	}

	*clock = clock.Add(2 * time.Second)
	got, _ = cache.Get(ctx, "k")
	if got.RateLimitPerMinute != 50 || src.calls.Load() != 2 {
		t.Fatalf("after TTL: limit=%d calls=%d, want refreshed 50 with 2 calls", got.RateLimitPerMinute, src.calls.Load())
	}
}

func TestTenantCache_DeletedTenantIsEvicted(t *testing.T) {
	src := &fakeLookup{tenants: map[string]*tenant.Tenant{}}
	src.set("k", &tenant.Tenant{ID: "k"})
	cache, clock := newTestCache(src, time.Second)
	ctx := context.Background()

	if _, err := cache.Get(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	src.set("k", nil)
	*clock = clock.Add(2 * time.Second)

	if _, err := cache.Get(ctx, "k"); !errors.Is(err, tenant.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound after delete + TTL", err)
	}
}

func TestTenantCache_ServesStaleWhenSourceFails(t *testing.T) {
	src := &fakeLookup{tenants: map[string]*tenant.Tenant{}}
	src.set("k", &tenant.Tenant{ID: "k", RateLimitPerMinute: 7})
	cache, clock := newTestCache(src, time.Second)
	ctx := context.Background()

	if _, err := cache.Get(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	src.err = errors.New("redis down")
	*clock = clock.Add(2 * time.Second)

	got, err := cache.Get(ctx, "k")
	if err != nil || got.RateLimitPerMinute != 7 {
		t.Fatalf("got %+v, %v; want stale tenant", got, err)
	}
	if _, err := cache.Get(ctx, "never-seen"); err == nil || errors.Is(err, tenant.ErrNotFound) {
		t.Fatalf("uncached key during outage: err = %v, want the source error", err)
	}
}

func TestTenantCache_ConcurrentMissesCollapse(t *testing.T) {
	src := &fakeLookup{
		tenants: map[string]*tenant.Tenant{},
		delay:   50 * time.Millisecond,
	}
	src.set("k", &tenant.Tenant{ID: "k"})
	cache, _ := newTestCache(src, time.Minute)

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cache.Get(context.Background(), "k"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if n := src.calls.Load(); n != 1 {
		t.Fatalf("source called %d times for 50 concurrent misses, want 1", n)
	}
}
