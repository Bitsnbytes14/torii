// Package tenant holds tenant config in Redis. The control plane is the only
// writer; gateways only read, so Redis is the single source of truth that
// every gateway replica converges on.
package tenant

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var ErrNotFound = errors.New("tenant not found")

// Tenant is the public view: it never carries the API key, plaintext or
// hashed. Only api_key_last_four exists, so an operator can visually
// confirm which key a caller is holding without the store ever giving up
// enough to authenticate as the tenant.
type Tenant struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	APIKeyLastFour     string    `json:"api_key_last_four"`
	RateLimitPerMinute int       `json:"rate_limit_per_minute"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// record is the on-disk shape in Redis. apiKeyHash never leaves this package;
// callers only ever get back a *Tenant.
type record struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	APIKeyHash         string    `json:"api_key_hash"`
	APIKeyLastFour     string    `json:"api_key_last_four"`
	RateLimitPerMinute int       `json:"rate_limit_per_minute"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (r record) tenant() *Tenant {
	return &Tenant{
		ID:                 r.ID,
		Name:               r.Name,
		APIKeyLastFour:     r.APIKeyLastFour,
		RateLimitPerMinute: r.RateLimitPerMinute,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
	}
}

// Key layout:
//
//	tenant:{tenant_id}       -> record JSON (name, rate limit, api key hash +
//	                            last four, timestamps; never the plaintext key)
//	apikey:{sha256(api_key)} -> tenant_id  (the only way from a presented key to
//	                            a tenant; a Redis dump or RDB backup leaks no
//	                            usable credential)
//	tenants                  -> set of tenant_ids (listing without KEYS/SCAN,
//	                            which walk the whole keyspace and would also
//	                            match the tenant:{id}:ratelimit:* counters)
const tenantsSetKey = "tenants"

func tenantKey(id string) string        { return "tenant:" + id }
func apiKeyIndexKey(hash string) string { return "apikey:" + hash }

// HashAPIKey is exported so callers that need to key off a presented API key
// (the gateway's tenant cache, in particular) can do so without ever storing
// or logging the plaintext.
func HashAPIKey(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])
}

func lastFour(apiKey string) string {
	if len(apiKey) <= 4 {
		return apiKey
	}
	return apiKey[len(apiKey)-4:]
}

type Store struct {
	rdb *redis.Client
}

func NewStore(rdb *redis.Client) *Store {
	return &Store{rdb: rdb}
}

// Create returns the new tenant plus its plaintext API key. That return value
// is the only time the plaintext key exists outside the caller's own memory;
// the store never persists it, so it cannot be recovered afterward.
func (s *Store) Create(ctx context.Context, name string, limit int) (*Tenant, string, error) {
	apiKey, err := newAPIKey()
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	rec := record{
		ID:                 uuid.NewString(),
		Name:               name,
		APIKeyHash:         HashAPIKey(apiKey),
		APIKeyLastFour:     lastFour(apiKey),
		RateLimitPerMinute: limit,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return nil, "", err
	}

	// MULTI/EXEC so a crash can't leave a tenant that authenticates but is
	// missing from the listing or the API-key index.
	_, err = s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, tenantKey(rec.ID), data, 0)
		p.Set(ctx, apiKeyIndexKey(rec.APIKeyHash), rec.ID, 0)
		p.SAdd(ctx, tenantsSetKey, rec.ID)
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("create tenant: %w", err)
	}
	return rec.tenant(), apiKey, nil
}

func (s *Store) GetByID(ctx context.Context, id string) (*Tenant, error) {
	data, err := s.rdb.Get(ctx, tenantKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("decode tenant: %w", err)
	}
	return rec.tenant(), nil
}

// GetByAPIKeyHash is what the gateway calls: it hashes the incoming
// X-API-Key header itself and never has to hand the plaintext to this
// package at all.
func (s *Store) GetByAPIKeyHash(ctx context.Context, hash string) (*Tenant, error) {
	id, err := s.rdb.Get(ctx, apiKeyIndexKey(hash)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolve api key: %w", err)
	}
	return s.GetByID(ctx, id)
}

// GetByAPIKey is a convenience wrapper for callers holding the plaintext
// (tests, the seed script's own verification, etc).
func (s *Store) GetByAPIKey(ctx context.Context, apiKey string) (*Tenant, error) {
	return s.GetByAPIKeyHash(ctx, HashAPIKey(apiKey))
}

func (s *Store) List(ctx context.Context) ([]Tenant, error) {
	ids, err := s.rdb.SMembers(ctx, tenantsSetKey).Result()
	if err != nil {
		return nil, fmt.Errorf("list tenant ids: %w", err)
	}
	tenants := make([]Tenant, 0, len(ids))
	if len(ids) == 0 {
		return tenants, nil
	}

	redisKeys := make([]string, len(ids))
	for i, id := range ids {
		redisKeys[i] = tenantKey(id)
	}
	vals, err := s.rdb.MGet(ctx, redisKeys...).Result()
	if err != nil {
		return nil, fmt.Errorf("load tenants: %w", err)
	}
	for _, v := range vals {
		str, ok := v.(string)
		if !ok {
			continue // deleted between SMEMBERS and MGET
		}
		var rec record
		if err := json.Unmarshal([]byte(str), &rec); err != nil {
			return nil, fmt.Errorf("decode tenant: %w", err)
		}
		tenants = append(tenants, *rec.tenant())
	}
	return tenants, nil
}

func (s *Store) UpdateRateLimit(ctx context.Context, id string, limit int) (*Tenant, error) {
	data, err := s.rdb.Get(ctx, tenantKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("decode tenant: %w", err)
	}
	rec.RateLimitPerMinute = limit
	rec.UpdatedAt = time.Now().UTC()
	newData, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}

	// SET XX only writes if the key still exists, so a PATCH racing a DELETE
	// can't resurrect a half-deleted tenant that's missing from the API-key
	// index and listing but still readable by ID.
	ok, err := s.rdb.SetXX(ctx, tenantKey(id), newData, redis.KeepTTL).Result()
	if err != nil {
		return nil, fmt.Errorf("update tenant: %w", err)
	}
	if !ok {
		return nil, ErrNotFound
	}
	return rec.tenant(), nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	data, err := s.rdb.Get(ctx, tenantKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get tenant: %w", err)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return fmt.Errorf("decode tenant: %w", err)
	}
	_, err = s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, tenantKey(id), apiKeyIndexKey(rec.APIKeyHash))
		p.SRem(ctx, tenantsSetKey, id)
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete tenant: %w", err)
	}
	return nil
}

// newAPIKey uses crypto/rand because the key is the tenant's only credential;
// a math/rand key would be guessable from a handful of observed ones.
func newAPIKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return "torii_" + hex.EncodeToString(b), nil
}
