package controlplane

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Bitsnbytes14/torii/internal/redistest"
	"github.com/Bitsnbytes14/torii/internal/tenant"
)

const testAdminToken = "test-admin-token"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := tenant.NewStore(redistest.New(t))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(NewHandler(store, logger, testAdminToken).Routes())
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	return doWithToken(t, method, url, body, testAdminToken)
}

func doWithToken(t *testing.T, method, url, body, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestTenantCRUD(t *testing.T) {
	srv := newTestServer(t)

	res := do(t, http.MethodPost, srv.URL+"/tenants", `{"name":"acme","rate_limit_per_minute":5}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", res.StatusCode)
	}
	var created struct {
		tenant.Tenant
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.APIKey == "" || created.RateLimitPerMinute != 5 {
		t.Fatalf("unexpected created tenant: %+v", created)
	}
	if !strings.HasSuffix(created.APIKey, created.APIKeyLastFour) {
		t.Fatalf("api_key_last_four %q isn't a suffix of returned key %q", created.APIKeyLastFour, created.APIKey)
	}

	// The one hard security requirement of this endpoint: no listing, at any
	// point, ever repeats the plaintext key handed back at creation.
	res = do(t, http.MethodGet, srv.URL+"/tenants", "")
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), created.APIKey) {
		t.Fatalf("GET /tenants leaked the plaintext api key: %s", body)
	}
	var list []tenant.Tenant
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	// Check membership, not length: with TEST_REDIS_URL (as in CI) the Redis
	// is shared with other test packages running in parallel.
	found := false
	for _, tn := range list {
		found = found || tn.ID == created.ID
	}
	if !found {
		t.Fatalf("created tenant %s missing from list %+v", created.ID, list)
	}

	res = do(t, http.MethodPatch, srv.URL+"/tenants/"+created.ID, `{"rate_limit_per_minute":50}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", res.StatusCode)
	}
	patchedBody, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(patchedBody), created.APIKey) {
		t.Fatalf("PATCH /tenants/{id} leaked the plaintext api key: %s", patchedBody)
	}
	var patched tenant.Tenant
	_ = json.Unmarshal(patchedBody, &patched)
	if patched.RateLimitPerMinute != 50 || patched.APIKeyLastFour != created.APIKeyLastFour {
		t.Fatalf("patched = %+v", patched)
	}

	if res := do(t, http.MethodDelete, srv.URL+"/tenants/"+created.ID, ""); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", res.StatusCode)
	}
	if res := do(t, http.MethodDelete, srv.URL+"/tenants/"+created.ID, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", res.StatusCode)
	}
}

func TestTenantValidation(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"missing name", http.MethodPost, "/tenants", `{"rate_limit_per_minute":5}`, http.StatusBadRequest},
		{"zero limit", http.MethodPost, "/tenants", `{"name":"x","rate_limit_per_minute":0}`, http.StatusBadRequest},
		{"patch without limit", http.MethodPatch, "/tenants/abc", `{}`, http.StatusBadRequest},
		{"patch unknown tenant", http.MethodPatch, "/tenants/abc", `{"rate_limit_per_minute":3}`, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if res := do(t, tc.method, srv.URL+tc.path, tc.body); res.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", res.StatusCode, tc.want)
			}
		})
	}
}

func TestAdminTokenRequired(t *testing.T) {
	srv := newTestServer(t)

	for _, token := range []string{"", "wrong-token", testAdminToken + "x"} {
		for _, tc := range []struct{ method, path, body string }{
			{http.MethodGet, "/tenants", ""},
			{http.MethodPost, "/tenants", `{"name":"x","rate_limit_per_minute":5}`},
			{http.MethodPatch, "/tenants/abc", `{"rate_limit_per_minute":3}`},
			{http.MethodDelete, "/tenants/abc", ""},
		} {
			res := doWithToken(t, tc.method, srv.URL+tc.path, tc.body, token)
			if res.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s with token %q: status %d, want 401", tc.method, tc.path, token, res.StatusCode)
			}
		}
	}

	// Probes from the kubelet carry no credentials, so health must stay open.
	if res := doWithToken(t, http.MethodGet, srv.URL+"/healthz", "", ""); res.StatusCode != http.StatusOK {
		t.Errorf("/healthz without token: status %d, want 200", res.StatusCode)
	}
}
