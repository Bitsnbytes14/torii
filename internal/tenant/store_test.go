package tenant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Bitsnbytes14/torii/internal/redistest"
)

func TestStore_Lifecycle(t *testing.T) {
	rdb := redistest.New(t)
	store := NewStore(rdb)
	ctx := context.Background()

	created, apiKey, err := store.Create(ctx, "acme", 10)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(apiKey, "torii_") || created.ID == "" {
		t.Fatalf("unexpected tenant identity: %+v key=%q", created, apiKey)
	}
	if created.APIKeyLastFour != apiKey[len(apiKey)-4:] {
		t.Fatalf("api_key_last_four = %q, want suffix of %q", created.APIKeyLastFour, apiKey)
	}

	byKey, err := store.GetByAPIKey(ctx, apiKey)
	if err != nil || byKey.ID != created.ID {
		t.Fatalf("GetByAPIKey = %+v, %v", byKey, err)
	}
	if _, err := store.GetByAPIKey(ctx, "torii_wrong-key"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByAPIKey with wrong key: err = %v, want ErrNotFound", err)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !containsID(list, created.ID) {
		t.Fatalf("list %+v missing created tenant", list)
	}

	updated, err := store.UpdateRateLimit(ctx, created.ID, 99)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.RateLimitPerMinute != 99 {
		t.Fatalf("limit = %d, want 99", updated.RateLimitPerMinute)
	}
	reread, _ := store.GetByAPIKey(ctx, apiKey)
	if reread.RateLimitPerMinute != 99 {
		t.Fatalf("update not persisted: limit = %d", reread.RateLimitPerMinute)
	}

	if err := store.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetByAPIKey(ctx, apiKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByAPIKey after delete: err = %v, want ErrNotFound", err)
	}
	if n, err := rdb.Exists(ctx, "apikey:"+HashAPIKey(apiKey)).Result(); err != nil || n != 0 {
		t.Fatalf("apikey index still exists after delete: n=%d err=%v", n, err)
	}
	list, _ = store.List(ctx)
	if containsID(list, created.ID) {
		t.Fatalf("deleted tenant still listed")
	}
}

func TestStore_UnknownIDs(t *testing.T) {
	store := NewStore(redistest.New(t))
	ctx := context.Background()

	if _, err := store.UpdateRateLimit(ctx, "nope", 5); !errors.Is(err, ErrNotFound) {
		t.Errorf("update unknown: err = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete unknown: err = %v, want ErrNotFound", err)
	}
}

func containsID(ts []Tenant, id string) bool {
	for _, t := range ts {
		if t.ID == id {
			return true
		}
	}
	return false
}
