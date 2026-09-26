// Package e2e runs against a live `docker compose up` stack: two real gateway
// processes, the control plane and Redis. The in-process tests in
// internal/gateway prove the logic; these prove the deployed wiring (same
// Redis, same env on both replicas) actually matches it.
//
// Opt-in with TORII_E2E=1 so `go test ./...` never depends on Docker.
package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
)

var (
	gatewayA     = envOr("GATEWAY_A_URL", "http://localhost:8080")
	gatewayB     = envOr("GATEWAY_B_URL", "http://localhost:8081")
	controlPlane = envOr("CONTROLPLANE_URL", "http://localhost:8082")
	adminToken   = envOr("CONTROLPLANE_ADMIN_TOKEN", "dev-admin-token")
)

type tenantResp struct {
	ID     string `json:"id"`
	APIKey string `json:"api_key"`
}

func TestE2E_RateLimitSharedAcrossGateways(t *testing.T) {
	requireE2E(t)
	const limit = 5
	const requests = 40
	tn := createTenant(t, limit)
	waitForFreshWindow(t)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		okByGW   = map[string]int{}
		limited  int
		statuses []int
	)
	start := make(chan struct{})
	for i := range requests {
		gw := gatewayA
		if i%2 == 1 {
			gw = gatewayB
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			status := getEvents(t, gw, tn.APIKey)
			mu.Lock()
			defer mu.Unlock()
			switch status {
			case http.StatusOK:
				okByGW[gw]++
			case http.StatusTooManyRequests:
				limited++
			default:
				statuses = append(statuses, status)
			}
		}()
	}
	close(start)
	wg.Wait()

	total := okByGW[gatewayA] + okByGW[gatewayB]
	t.Logf("successes: gateway-a=%d gateway-b=%d, rate limited=%d", okByGW[gatewayA], okByGW[gatewayB], limited)
	if len(statuses) > 0 {
		t.Fatalf("unexpected statuses: %v", statuses)
	}
	if total > limit {
		t.Fatalf("RATE LIMIT BYPASSED: %d successes across both gateways, limit is %d", total, limit)
	}
	if total != limit {
		t.Fatalf("successes = %d, want exactly %d", total, limit)
	}
}

func TestE2E_HotReloadWithoutRestart(t *testing.T) {
	requireE2E(t)
	tn := createTenant(t, 2)
	waitForFreshWindow(t)

	getEvents(t, gatewayA, tn.APIKey)
	getEvents(t, gatewayB, tn.APIKey)
	if s := getEvents(t, gatewayA, tn.APIKey); s != http.StatusTooManyRequests {
		t.Fatalf("3rd request at limit 2: status %d, want 429", s)
	}

	patchLimit(t, tn.ID, 20)
	patchedAt := time.Now()

	// Wait out the 5s tenant cache TTL before probing rather than polling
	// through it: rejected requests still count toward the window, so tight
	// polling would push the counter past the new limit before it loads.
	time.Sleep(5 * time.Second)
	for range 5 {
		if getEvents(t, gatewayB, tn.APIKey) == http.StatusOK {
			t.Logf("new limit took effect within %v of PATCH", time.Since(patchedAt).Round(100*time.Millisecond))
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("new limit not picked up within 10s of PATCH")
}

func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("TORII_E2E") != "1" {
		t.Skip("set TORII_E2E=1 with `docker compose up` running to run end-to-end tests")
	}
}

// waitForFreshWindow avoids starting a burst in the last seconds of a minute,
// where the fixed window would reset mid-test and legitimately allow 2x the
// limit, making the assertion flaky for reasons unrelated to the code.
func waitForFreshWindow(t *testing.T) {
	t.Helper()
	if s := time.Now().Second(); s >= 45 {
		wait := time.Duration(61-s) * time.Second
		t.Logf("near minute boundary, waiting %v for a fresh rate-limit window", wait)
		time.Sleep(wait)
	}
}

func createTenant(t *testing.T, limit int) tenantResp {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": "e2e-" + t.Name(), "rate_limit_per_minute": limit})
	req, _ := http.NewRequest(http.MethodPost, controlPlane+"/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := adminDo(req)
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create tenant: status %d", res.StatusCode)
	}
	var tn tenantResp
	if err := json.NewDecoder(res.Body).Decode(&tn); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodDelete, controlPlane+"/tenants/"+tn.ID, nil)
		if res, err := adminDo(req); err == nil {
			res.Body.Close()
		}
	})
	return tn
}

func patchLimit(t *testing.T, id string, limit int) {
	t.Helper()
	body, _ := json.Marshal(map[string]int{"rate_limit_per_minute": limit})
	req, _ := http.NewRequest(http.MethodPatch, controlPlane+"/tenants/"+id, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := adminDo(req)
	if err != nil {
		t.Fatalf("patch tenant: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("patch tenant: status %d", res.StatusCode)
	}
}

func getEvents(t *testing.T, gateway, apiKey string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, gateway+"/api/events", nil)
	req.Header.Set("X-API-Key", apiKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Errorf("GET %s/api/events: %v", gateway, err)
		return 0
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode
}

func adminDo(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+adminToken)
	return http.DefaultClient.Do(req)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
