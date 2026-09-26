package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Bitsnbytes14/torii/internal/redistest"
	"github.com/Bitsnbytes14/torii/internal/tenant"
)

// replica is one gateway instance: its own router, tenant cache and limiter,
// sharing nothing with other replicas except the Redis client they're given.
type replica struct {
	srv   *httptest.Server
	cache *TenantCache
}

func startReplica(t *testing.T, rdb *redis.Client, upstream *url.URL, cacheTTL time.Duration, clock func() time.Time) *replica {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cache := NewTenantCache(tenant.NewStore(rdb), cacheTTL, logger)
	limiter := NewRateLimiter(rdb)
	// Pinning the limiter clock keeps every request in one window, so the
	// assertions can't be broken by the test happening to straddle a minute.
	limiter.now = clock

	srv := httptest.NewServer(NewRouter(Config{
		BookingURL: upstream,
		WebDir:     t.TempDir(),
		JWTSecret:  []byte("test-secret"),
		Tenants:    cache,
		Limiter:    limiter,
		Logger:     logger,
	}))
	t.Cleanup(srv.Close)
	return &replica{srv: srv, cache: cache}
}

// startUpstream stands in for the booking service and records whether the
// tenant credential leaked through the proxy.
func startUpstream(t *testing.T) (*url.URL, *atomic.Bool) {
	t.Helper()
	var leaked atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(apiKeyHeader) != "" {
			leaked.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u, &leaked
}

func get(t *testing.T, baseURL, apiKey string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/events", nil)
	if apiKey != "" {
		req.Header.Set(apiKeyHeader, apiKey)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res
}

func fixedClock() func() time.Time {
	now := time.Now()
	return func() time.Time { return now }
}

// TestRateLimit_SharedAcrossReplicas is the Phase 3 counterpart to the
// booking overbooking test. Two gateway instances with separate in-process
// state take a concurrent burst for the same tenant; if the limit lived in
// process memory each would allow its own 5 (10 total). Because the counter
// lives in Redis, the total across both must never exceed 5.
func TestRateLimit_SharedAcrossReplicas(t *testing.T) {
	rdb := redistest.New(t)
	upstream, _ := startUpstream(t)
	clock := fixedClock()
	a := startReplica(t, rdb, upstream, 5*time.Second, clock)
	b := startReplica(t, rdb, upstream, 5*time.Second, clock)

	const limit = 5
	const requests = 40
	_, apiKey, err := tenant.NewStore(rdb).Create(context.Background(), "replica-test", limit)
	if err != nil {
		t.Fatal(err)
	}

	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		okByReplica = map[string]int{}
		limited     int
		other       []int
	)
	start := make(chan struct{})
	for i := range requests {
		target, name := a.srv.URL, "a"
		if i%2 == 1 {
			target, name = b.srv.URL, "b"
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines together to maximize contention
			res := get(t, target, apiKey)
			mu.Lock()
			defer mu.Unlock()
			switch res.StatusCode {
			case http.StatusOK:
				okByReplica[name]++
			case http.StatusTooManyRequests:
				limited++
				if res.Header.Get("Retry-After") == "" {
					t.Error("429 without Retry-After header")
				}
			default:
				other = append(other, res.StatusCode)
			}
		}()
	}
	close(start)
	wg.Wait()

	total := okByReplica["a"] + okByReplica["b"]
	t.Logf("successes: gateway-a=%d gateway-b=%d, rate limited=%d", okByReplica["a"], okByReplica["b"], limited)

	if len(other) > 0 {
		t.Fatalf("unexpected statuses: %v", other)
	}
	if total > limit {
		t.Fatalf("RATE LIMIT BYPASSED: %d successes across both replicas, limit is %d", total, limit)
	}
	if total != limit || limited != requests-limit {
		t.Fatalf("successes=%d limited=%d, want exactly %d and %d", total, limited, limit, requests-limit)
	}
}

// TestRateLimit_HotReload changes a tenant's limit in Redis mid-flight, the
// way the control plane's PATCH does, and checks both replicas pick it up
// within one cache TTL with no restart.
func TestRateLimit_HotReload(t *testing.T) {
	rdb := redistest.New(t)
	upstream, _ := startUpstream(t)
	clock := fixedClock()
	const ttl = 200 * time.Millisecond
	a := startReplica(t, rdb, upstream, ttl, clock)
	b := startReplica(t, rdb, upstream, ttl, clock)

	store := tenant.NewStore(rdb)
	tn, apiKey, err := store.Create(context.Background(), "hot-reload-test", 2)
	if err != nil {
		t.Fatal(err)
	}

	for i, r := range []*replica{a, b} {
		if res := get(t, r.srv.URL, apiKey); res.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status %d, want 200", i+1, res.StatusCode)
		}
	}
	res := get(t, a.srv.URL, apiKey)
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("3rd request: status %d, want 429 at limit 2", res.StatusCode)
	}

	if _, err := store.UpdateRateLimit(context.Background(), tn.ID, 10); err != nil {
		t.Fatal(err)
	}

	// Inside the TTL the replicas still hold the old config: this is the
	// staleness the cache trades for not reading Redis on every request.
	if res := get(t, b.srv.URL, apiKey); res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("immediately after PATCH: status %d, want 429 from cached limit", res.StatusCode)
	}

	time.Sleep(ttl + 50*time.Millisecond)

	for _, r := range []*replica{a, b} {
		res := get(t, r.srv.URL, apiKey)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("after TTL: status %d, want 200 under new limit", res.StatusCode)
		}
		if got := res.Header.Get("X-RateLimit-Limit"); got != strconv.Itoa(10) {
			t.Fatalf("X-RateLimit-Limit = %q, want 10", got)
		}
	}
}

func TestTenantAuth(t *testing.T) {
	rdb := redistest.New(t)
	upstream, leaked := startUpstream(t)
	gw := startReplica(t, rdb, upstream, time.Second, fixedClock())

	_, apiKey, err := tenant.NewStore(rdb).Create(context.Background(), "auth-test", 100)
	if err != nil {
		t.Fatal(err)
	}

	if res := get(t, gw.srv.URL, ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing key: status %d, want 401", res.StatusCode)
	}
	if res := get(t, gw.srv.URL, "torii_not-a-real-key"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("unknown key: status %d, want 401", res.StatusCode)
	}
	if res := get(t, gw.srv.URL, apiKey); res.StatusCode != http.StatusOK {
		t.Errorf("valid key: status %d, want 200", res.StatusCode)
	}
	if leaked.Load() {
		t.Errorf("X-API-Key was forwarded to the upstream service")
	}

	// Health and the static frontend stay outside tenant auth.
	res, err := http.Get(gw.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("/healthz without key: status %d, want 200", res.StatusCode)
	}
}
