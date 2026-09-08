package client_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TheDJVG/BCRSTracking/internal/client"
	"github.com/TheDJVG/BCRSTracking/internal/config"
	"github.com/TheDJVG/BCRSTracking/internal/metrics"
)

func mockTokenServerHelper(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) bool {
	if r.URL.Path == "/forapi/v2/locations/access-token" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"ok","data":{"token":%q,"expiresAt":%q}}`, token, expiresAt.Format(time.RFC3339))
		return true
	}
	return false
}

func TestGetLocationsSuccess(t *testing.T) {
	var capturedUserAgent string
	var capturedAccept string
	var capturedClient string
	var capturedReferer string
	var capturedToken string
	var tokenCalls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			atomic.AddInt32(&tokenCalls, 1)
			if r.Header.Get("x-bcrs-map-token") != "" {
				t.Errorf("token request should not have x-bcrs-map-token header")
			}
			if r.Header.Get("x-bcrs-client") != "web" {
				t.Errorf("expected x-bcrs-client 'web' on token request, got %q", r.Header.Get("x-bcrs-client"))
			}
			if r.Header.Get("Referer") != "https://bts.bcrs.sg/" {
				t.Errorf("expected Referer 'https://bts.bcrs.sg/' on token request, got %q", r.Header.Get("Referer"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":{"token":"test-token-loc","expiresAt":"2099-01-01T00:00:00Z"}}`))
			return
		}

		if r.URL.Path != "/forapi/v2/locations" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		capturedUserAgent = r.Header.Get("User-Agent")
		capturedAccept = r.Header.Get("Accept")
		capturedClient = r.Header.Get("x-bcrs-client")
		capturedReferer = r.Header.Get("Referer")
		capturedToken = r.Header.Get("x-bcrs-map-token")

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","data":[{"id":101,"serialNumber":"S1","supplierId":"SUP1","locationName":"Loc 1","latitude":"1.3","longitude":"103.8","coords_color":"Red","address":"Addr 1","postalCode":"111111","status":"RUNNING","rvmOpeningHours":"24h"}]}`))
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   10,
		RateLimitBurst: 20,
	}
	m := metrics.New()
	c := client.New(cfg, m)

	locations, err := c.GetLocations(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(locations) != 1 || locations[0].ID != 101 {
		t.Fatalf("expected 1 location with ID 101, got %+v", locations)
	}
	if locations[0].LocationName != "Loc 1" {
		t.Errorf("expected locationName 'Loc 1', got %q", locations[0].LocationName)
	}
	if capturedUserAgent != "BCRSTracking/1.0 (+https://github.com/TheDJVG/BCRSTracking)" {
		t.Errorf("expected User-Agent 'BCRSTracking/1.0 (+https://github.com/TheDJVG/BCRSTracking)', got %q", capturedUserAgent)
	}
	if capturedAccept != "application/json" {
		t.Errorf("expected Accept 'application/json', got %q", capturedAccept)
	}
	if capturedClient != "web" {
		t.Errorf("expected x-bcrs-client 'web', got %q", capturedClient)
	}
	if capturedReferer != "https://bts.bcrs.sg/" {
		t.Errorf("expected Referer 'https://bts.bcrs.sg/', got %q", capturedReferer)
	}
	if capturedToken != "test-token-loc" {
		t.Errorf("expected x-bcrs-map-token 'test-token-loc', got %q", capturedToken)
	}
	if atomic.LoadInt32(&tokenCalls) != 1 {
		t.Errorf("expected 1 token call, got %d", atomic.LoadInt32(&tokenCalls))
	}
}

func TestGetBinStatusSuccess(t *testing.T) {
	var capturedClient string
	var capturedReferer string
	var capturedToken string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mockTokenServerHelper(w, r, "bin-token-abc", time.Now().Add(1*time.Hour)) {
			return
		}
		if r.URL.Path != "/forapi/v2/locations/rvms/202/bin-status" {
			http.NotFound(w, r)
			return
		}
		capturedClient = r.Header.Get("x-bcrs-client")
		capturedReferer = r.Header.Get("Referer")
		capturedToken = r.Header.Get("x-bcrs-map-token")

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","data":[{"id":999,"rvm_id":202,"compartment_type":"Bottle Can","capacity":800,"current_count":120,"threshold_level":15,"last_updated":"2026-08-23T05:00:00Z"}]}`))
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m)

	bins, err := c.GetBinStatus(context.Background(), 202)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bins) != 1 || bins[0].ID != 999 || bins[0].RVMID != 202 || bins[0].CurrentCount != 120 {
		t.Fatalf("expected 1 bin record with count 120, got %+v", bins)
	}
	if capturedClient != "web" {
		t.Errorf("expected x-bcrs-client 'web', got %q", capturedClient)
	}
	if capturedReferer != "https://bts.bcrs.sg/" {
		t.Errorf("expected Referer 'https://bts.bcrs.sg/', got %q", capturedReferer)
	}
	if capturedToken != "bin-token-abc" {
		t.Errorf("expected x-bcrs-map-token 'bin-token-abc', got %q", capturedToken)
	}
}

func TestGetBinStatusWith429Retry(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mockTokenServerHelper(w, r, "token-429", time.Now().Add(1*time.Hour)) {
			return
		}
		if r.URL.Path == "/forapi/v2/locations/rvms/101/bin-status" {
			if atomic.AddInt32(&attempts, 1) == 1 {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":[{"id":999,"rvm_id":101,"compartment_type":"Bottle Can","capacity":800,"current_count":120,"threshold_level":15,"last_updated":"2026-08-23T05:00:00Z"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	bins, err := c.GetBinStatus(context.Background(), 101)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bins) != 1 || bins[0].CurrentCount != 120 {
		t.Fatalf("expected 1 bin record with count 120, got %+v", bins)
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Errorf("expected 2 attempts after 429 retry, got %d", atomic.LoadInt32(&attempts))
	}
}

func TestGetLocationsRetryOn5xx(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mockTokenServerHelper(w, r, "token-5xx", time.Now().Add(1*time.Hour)) {
			return
		}
		if r.URL.Path == "/forapi/v2/locations" {
			att := atomic.AddInt32(&attempts, 1)
			if att == 1 {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			if att == 2 {
				http.Error(w, "Bad Gateway", http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":[{"id":101,"serialNumber":"S1","supplierId":"SUP1","locationName":"Loc 1","latitude":"1.3","longitude":"103.8","coords_color":"Red","address":"Addr 1","postalCode":"111111","status":"RUNNING","rvmOpeningHours":"24h"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	locations, err := c.GetLocations(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(locations) != 1 || locations[0].ID != 101 {
		t.Fatalf("expected 1 location with ID 101, got %+v", locations)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", atomic.LoadInt32(&attempts))
	}
}

func TestRetryExhaustion429(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mockTokenServerHelper(w, r, "token-exhaust", time.Now().Add(1*time.Hour)) {
			return
		}
		if r.URL.Path == "/forapi/v2/locations" {
			atomic.AddInt32(&attempts, 1)
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	_, err := c.GetLocations(context.Background())
	if err == nil {
		t.Fatalf("expected error after retry exhaustion, got nil")
	}
	if atomic.LoadInt32(&attempts) != 4 { // Initial attempt (0) + 3 retries (1, 2, 3) = 4
		t.Errorf("expected 4 attempts (1 initial + 3 retries), got %d", atomic.LoadInt32(&attempts))
	}
}

func TestRetryExhaustion5xx(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mockTokenServerHelper(w, r, "token-5xx-ex", time.Now().Add(1*time.Hour)) {
			return
		}
		if r.URL.Path == "/forapi/v2/locations/rvms/101/bin-status" {
			atomic.AddInt32(&attempts, 1)
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	_, err := c.GetBinStatus(context.Background(), 101)
	if err == nil {
		t.Fatalf("expected error after retry exhaustion, got nil")
	}
	if atomic.LoadInt32(&attempts) != 4 {
		t.Errorf("expected 4 attempts, got %d", atomic.LoadInt32(&attempts))
	}
}

func TestNonRetryable4xx(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mockTokenServerHelper(w, r, "token-404", time.Now().Add(1*time.Hour)) {
			return
		}
		if r.URL.Path == "/forapi/v2/locations/rvms/101/bin-status" {
			atomic.AddInt32(&attempts, 1)
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	_, err := c.GetBinStatus(context.Background(), 101)
	if err == nil {
		t.Fatalf("expected error on 404, got nil")
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Errorf("expected exactly 1 attempt for 404 status, got %d", atomic.LoadInt32(&attempts))
	}
}

func TestInvalidJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mockTokenServerHelper(w, r, "token-valid", time.Now().Add(1*time.Hour)) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{not-valid-json`))
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m)

	_, err := c.GetLocations(context.Background())
	if err == nil {
		t.Fatalf("expected JSON unmarshal error, got nil")
	}

	_, err = c.GetBinStatus(context.Background(), 101)
	if err == nil {
		t.Fatalf("expected JSON unmarshal error, got nil")
	}
}

func TestContextCancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","data":[]}`))
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := c.GetLocations(ctx)
	if err == nil {
		t.Fatalf("expected error on cancelled context, got nil")
	}
}

func TestRateLimiter(t *testing.T) {
	var count int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mockTokenServerHelper(w, r, "token-rate", time.Now().Add(1*time.Hour)) {
			return
		}
		atomic.AddInt32(&count, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","data":[]}`))
	}))
	defer ts.Close()

	// 5 RPS with burst of 1 -> 3 requests should take >= 350ms
	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   5,
		RateLimitBurst: 1,
	}
	m := metrics.New()
	c := client.New(cfg, m)

	start := time.Now()
	for i := 0; i < 3; i++ {
		_, err := c.GetLocations(context.Background())
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	if elapsed < 350*time.Millisecond {
		t.Errorf("expected rate limiting to take at least 350ms for 3 requests at 5 RPS, took %v", elapsed)
	}
}

func TestTokenAcquisitionAndCaching(t *testing.T) {
	var tokenCalls int32
	expectedExpiry := time.Now().Add(10 * time.Minute).Truncate(time.Second)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			atomic.AddInt32(&tokenCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"status":"ok","data":{"token":"cached-token-123","expiresAt":%q}}`, expectedExpiry.Format(time.RFC3339))
			return
		}
		if r.URL.Path == "/forapi/v2/locations" {
			tok := r.Header.Get("x-bcrs-map-token")
			if tok != "cached-token-123" {
				t.Errorf("expected map-token 'cached-token-123', got %q", tok)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m)

	// Call 1: Fetches token
	_, err := c.GetLocations(context.Background())
	if err != nil {
		t.Fatalf("call 1 failed: %v", err)
	}

	tok, exp := c.CachedToken()
	if tok != "cached-token-123" {
		t.Errorf("expected cached token 'cached-token-123', got %q", tok)
	}
	if !exp.Equal(expectedExpiry) {
		t.Errorf("expected expiry %v, got %v", expectedExpiry, exp)
	}

	// Call 2 & Call 3: Should reuse cached token without fetching again
	for i := 2; i <= 3; i++ {
		_, err := c.GetLocations(context.Background())
		if err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
	}

	if calls := atomic.LoadInt32(&tokenCalls); calls != 1 {
		t.Errorf("expected exactly 1 token fetch call, got %d", calls)
	}
}

func TestTokenExpirationAndRenewal(t *testing.T) {
	var tokenCalls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			call := atomic.AddInt32(&tokenCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			if call == 1 {
				// Expires quickly (40ms from now)
				shortExpiry := time.Now().Add(40 * time.Millisecond)
				_, _ = fmt.Fprintf(w, `{"status":"ok","data":{"token":"token-v1","expiresAt":%q}}`, shortExpiry.Format(time.RFC3339Nano))
			} else {
				// Refreshed token with longer expiry
				longExpiry := time.Now().Add(1 * time.Hour)
				_, _ = fmt.Fprintf(w, `{"status":"ok","data":{"token":"token-v2","expiresAt":%q}}`, longExpiry.Format(time.RFC3339))
			}
			return
		}
		if r.URL.Path == "/forapi/v2/locations" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m)

	// Call 1: Gets token-v1
	_, err := c.GetLocations(context.Background())
	if err != nil {
		t.Fatalf("call 1 failed: %v", err)
	}
	tok, _ := c.CachedToken()
	if tok != "token-v1" {
		t.Fatalf("expected token-v1, got %q", tok)
	}

	// Sleep until token-v1 expires
	time.Sleep(60 * time.Millisecond)

	// Call 2: Token is expired, should refresh and get token-v2
	_, err = c.GetLocations(context.Background())
	if err != nil {
		t.Fatalf("call 2 failed: %v", err)
	}
	tok, _ = c.CachedToken()
	if tok != "token-v2" {
		t.Fatalf("expected refreshed token-v2, got %q", tok)
	}
	if calls := atomic.LoadInt32(&tokenCalls); calls != 2 {
		t.Errorf("expected 2 token calls (initial + refresh), got %d", calls)
	}
}

func TestTokenConcurrentAccess(t *testing.T) {
	var tokenCalls int32
	var locationCalls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			// Artificially simulate network latency to test single-flight / double-check lock
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&tokenCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":{"token":"concurrent-tok","expiresAt":"2099-01-01T00:00:00Z"}}`))
			return
		}
		if r.URL.Path == "/forapi/v2/locations" {
			tok := r.Header.Get("x-bcrs-map-token")
			if tok != "concurrent-tok" {
				t.Errorf("expected 'concurrent-tok', got %q", tok)
			}
			atomic.AddInt32(&locationCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    5 * time.Second,
		RateLimitRPS:   500,
		RateLimitBurst: 500,
	}
	m := metrics.New()
	c := client.New(cfg, m)

	numGoroutines := 50
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.GetLocations(context.Background())
			if err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent request failed: %v", err)
	}

	if calls := atomic.LoadInt32(&tokenCalls); calls != 1 {
		t.Errorf("expected exactly 1 token acquisition across concurrent requests, got %d", calls)
	}
	if locCalls := atomic.LoadInt32(&locationCalls); locCalls != int32(numGoroutines) {
		t.Errorf("expected %d location calls, got %d", numGoroutines, locCalls)
	}
}

func TestForbidden403InvalidationAndRetrySuccess(t *testing.T) {
	var tokenCalls int32
	var locCalls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			call := atomic.AddInt32(&tokenCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"status":"ok","data":{"token":"token-v%d","expiresAt":"2099-01-01T00:00:00Z"}}`, call)
			return
		}

		if r.URL.Path == "/forapi/v2/locations" {
			call := atomic.AddInt32(&locCalls, 1)
			tok := r.Header.Get("x-bcrs-map-token")

			// First request carries token-v1 which fails with 403 Forbidden
			if tok == "token-v1" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			// Subsequent request carries token-v2 which succeeds with 200
			if tok == "token-v2" {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"status":"ok","data":[{"id":42,"serialNumber":"M42"}]}`))
				return
			}

			http.Error(w, fmt.Sprintf("unexpected token: %s (call %d)", tok, call), http.StatusBadRequest)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	locations, err := c.GetLocations(context.Background())
	if err != nil {
		t.Fatalf("expected request to succeed after 403 retry with new token, got error: %v", err)
	}
	if len(locations) != 1 || locations[0].ID != 42 {
		t.Errorf("unexpected locations: %+v", locations)
	}

	if tok, _ := c.CachedToken(); tok != "token-v2" {
		t.Errorf("expected cached token to be 'token-v2', got %q", tok)
	}

	if atomic.LoadInt32(&tokenCalls) != 2 {
		t.Errorf("expected 2 token acquisitions (initial + renewed after 403), got %d", atomic.LoadInt32(&tokenCalls))
	}
	if atomic.LoadInt32(&locCalls) != 2 {
		t.Errorf("expected 2 location attempts (1 failed 403 + 1 successful retry), got %d", atomic.LoadInt32(&locCalls))
	}

	// Verify metrics recorded auth error
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)
	body := w.Body.String()

	if !strings.Contains(body, `bcrs_auth_errors_total{endpoint="/locations"} 1`) {
		t.Errorf("missing bcrs_auth_errors_total metric for /locations: %s", body)
	}
	if !strings.Contains(body, `bcrs_http_requests_total{endpoint="/locations",status_code="403"} 1`) {
		t.Errorf("missing 403 status code metric: %s", body)
	}
}

func TestForbidden403RetryExhaustion(t *testing.T) {
	var locAttempts int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mockTokenServerHelper(w, r, "token-always-403", time.Now().Add(1*time.Hour)) {
			return
		}
		if r.URL.Path == "/forapi/v2/locations" {
			atomic.AddInt32(&locAttempts, 1)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	_, err := c.GetLocations(context.Background())
	if err == nil {
		t.Fatal("expected error on exhausted 403 retries, got nil")
	}

	// 1 initial attempt + 3 retries = 4 attempts
	if attempts := atomic.LoadInt32(&locAttempts); attempts != 4 {
		t.Errorf("expected 4 attempts for 403 retry exhaustion, got %d", attempts)
	}

	// Cached token should be invalidated
	tok, _ := c.CachedToken()
	if tok != "" {
		t.Errorf("expected cached token to be invalidated (empty), got %q", tok)
	}

	// Verify metrics recorded 4 auth errors
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)
	body := w.Body.String()

	if !strings.Contains(body, `bcrs_auth_errors_total{endpoint="/locations"} 4`) {
		t.Errorf("expected 4 auth errors in metrics: %s", body)
	}
}

func TestForbidden403OnTokenEndpoint(t *testing.T) {
	var tokenAttempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			atomic.AddInt32(&tokenAttempts, 1)
			http.Error(w, "Forbidden Token", http.StatusForbidden)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	_, err := c.GetLocations(context.Background())
	if err == nil {
		t.Fatal("expected error when token endpoint returns 403, got nil")
	}

	// 1 initial attempt + 3 retries = 4 attempts on token endpoint
	if attempts := atomic.LoadInt32(&tokenAttempts); attempts != 4 {
		t.Errorf("expected 4 token attempts on 403 retry exhaustion, got %d", attempts)
	}
}

func TestExplicitTokenInvalidation(t *testing.T) {
	c := client.New(&config.Config{}, nil, client.WithToken("manual-tok", time.Now().Add(1*time.Hour)))

	tok, _ := c.CachedToken()
	if tok != "manual-tok" {
		t.Fatalf("expected manual-tok, got %q", tok)
	}

	c.InvalidateToken()

	tok, exp := c.CachedToken()
	if tok != "" || !exp.IsZero() {
		t.Errorf("expected token and expiry to be cleared, got tok=%q exp=%v", tok, exp)
	}
}

func TestTokenClockSkewFallback(t *testing.T) {
	var tokenCalls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			atomic.AddInt32(&tokenCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			// Return an expiresAt that is 5 minutes in the past relative to the host
			pastExpiry := time.Now().Add(-5 * time.Minute)
			_, _ = fmt.Fprintf(w, `{"status":"ok","data":{"token":"skewed-tok","expiresAt":%q}}`, pastExpiry.Format(time.RFC3339))
			return
		}
		if r.URL.Path == "/forapi/v2/locations" {
			tok := r.Header.Get("x-bcrs-map-token")
			if tok != "skewed-tok" {
				t.Errorf("expected map token 'skewed-tok', got %q", tok)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m)

	// Call 1: fetches token; clock skew detected, fallback expiry applied
	_, err := c.GetLocations(context.Background())
	if err != nil {
		t.Fatalf("call 1 failed: %v", err)
	}

	tok, exp := c.CachedToken()
	if tok != "skewed-tok" {
		t.Errorf("expected cached token 'skewed-tok', got %q", tok)
	}
	if !exp.After(time.Now()) {
		t.Errorf("expected fallback expiry to be in the future, got %v", exp)
	}

	// Call 2 & Call 3: should reuse token without re-fetching
	for i := 2; i <= 3; i++ {
		_, err := c.GetLocations(context.Background())
		if err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
	}

	if calls := atomic.LoadInt32(&tokenCalls); calls != 1 {
		t.Errorf("expected exactly 1 token call due to fallback caching, got %d", calls)
	}
}

func TestTokenEmptyTokenError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			w.Header().Set("Content-Type", "application/json")
			// Return empty string for token
			w.Write([]byte(`{"status":"ok","data":{"token":""}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	_, err := c.GetLocations(context.Background())
	if err == nil {
		t.Fatal("expected error when token is empty, got nil")
	}
	if !strings.Contains(err.Error(), "token not found") && !strings.Contains(err.Error(), "failed to obtain access token") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestTokenCancelledContext(t *testing.T) {
	c := client.New(&config.Config{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.GetLocations(ctx)
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
}

func TestTokenConcurrentFailureSingleFlight(t *testing.T) {
	var tokenAttempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			atomic.AddInt32(&tokenAttempts, 1)
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   500,
		RateLimitBurst: 500,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	numGoroutines := 20
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.GetLocations(context.Background())
			errCh <- err
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err == nil {
			t.Fatal("expected error for all concurrent requests when token endpoint fails, got nil")
		}
	}

	// Single-flight ensures that only 1 leader executes the fetch with 3 retries (total 4 attempts)
	// instead of 20 goroutines * 4 = 80 serialized attempts!
	if attempts := atomic.LoadInt32(&tokenAttempts); attempts != 4 {
		t.Errorf("expected 4 single-flight token attempts across all 20 concurrent requests, got %d", attempts)
	}
}

func TestTokenLeaderCancellationRecovery(t *testing.T) {
	var tokenCalls int32
	leaderStarted := make(chan struct{})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			call := atomic.AddInt32(&tokenCalls, 1)
			if call == 1 {
				close(leaderStarted)
				// Delay enough to allow leader's short context to expire
				time.Sleep(60 * time.Millisecond)
				http.Error(w, "timeout", http.StatusGatewayTimeout)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":{"token":"follower-token","expiresAt":"2099-01-01T00:00:00Z"}}`))
			return
		}
		if r.URL.Path == "/forapi/v2/locations" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":[{"id":1}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	// Leader with short context (30ms)
	leaderCtx, leaderCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer leaderCancel()

	// Follower with long context (2s)
	followerCtx, followerCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer followerCancel()

	var leaderErr, followerErr error
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, leaderErr = c.GetLocations(leaderCtx)
	}()

	// Wait until leader has started fetch
	<-leaderStarted

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, followerErr = c.GetLocations(followerCtx)
	}()

	wg.Wait()

	if leaderErr == nil {
		t.Errorf("expected leader to fail due to context deadline exceeded, got nil")
	}
	if followerErr != nil {
		t.Errorf("expected follower to recover and succeed after leader cancellation, got: %v", followerErr)
	}

	tok, _ := c.CachedToken()
	if tok != "follower-token" {
		t.Errorf("expected follower-token to be cached, got %q", tok)
	}
}

func TestTokenConcurrent403Deduplication(t *testing.T) {
	var tokenRenewals int32
	var locCalls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			atomic.AddInt32(&tokenRenewals, 1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":{"token":"token-good","expiresAt":"2099-01-01T00:00:00Z"}}`))
			return
		}
		if r.URL.Path == "/forapi/v2/locations" {
			atomic.AddInt32(&locCalls, 1)
			tok := r.Header.Get("x-bcrs-map-token")
			if tok == "token-bad" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			if tok == "token-good" {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"status":"ok","data":[{"id":1}]}`))
				return
			}
			http.Error(w, "bad token", http.StatusBadRequest)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   500,
		RateLimitBurst: 500,
	}
	m := metrics.New()
	// Pre-seed with token-bad
	c := client.New(cfg, m,
		client.WithToken("token-bad", time.Now().Add(1*time.Hour)),
		client.WithInitialBackoff(1*time.Millisecond),
	)

	numGoroutines := 10
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.GetLocations(context.Background())
			if err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("unexpected failure: %v", err)
	}

	// Token renewal should be single-flight deduplicated to 1 call
	if renewals := atomic.LoadInt32(&tokenRenewals); renewals != 1 {
		t.Errorf("expected exactly 1 token renewal across 10 concurrent 403s, got %d", renewals)
	}

	tok, _ := c.CachedToken()
	if tok != "token-good" {
		t.Errorf("expected cached token to be 'token-good', got %q", tok)
	}
}

func TestTokenExplicitInvalidationDuringFetch(t *testing.T) {
	fetchStarted := make(chan struct{})
	var tokenCalls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			call := atomic.AddInt32(&tokenCalls, 1)
			if call == 1 {
				close(fetchStarted)
				time.Sleep(50 * time.Millisecond)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"status":"ok","data":{"token":"stale-tok","expiresAt":"2099-01-01T00:00:00Z"}}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":{"token":"fresh-tok","expiresAt":"2099-01-01T00:00:00Z"}}`))
			return
		}
		if r.URL.Path == "/forapi/v2/locations" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = c.GetLocations(context.Background())
	}()

	// Wait for fetch to be underway
	<-fetchStarted

	// Explicitly invalidate while fetch is in-flight
	c.InvalidateToken()

	wg.Wait()

	// Stale token should not have overwritten the invalidation
	tok, _ := c.CachedToken()
	if tok == "stale-tok" {
		t.Errorf("expected stale-tok not to be cached after explicit InvalidateToken, got %q", tok)
	}
}

func TestTokenFollowerRecoveryOnInvalidationDuringFetch(t *testing.T) {
	fetchStarted := make(chan struct{})
	followerReady := make(chan struct{})
	var tokenCalls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			call := atomic.AddInt32(&tokenCalls, 1)
			if call == 1 {
				close(fetchStarted)
				// Wait for follower to join before continuing
				<-followerReady
				time.Sleep(30 * time.Millisecond)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"status":"ok","data":{"token":"stale-tok","expiresAt":"2099-01-01T00:00:00Z"}}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":{"token":"fresh-tok","expiresAt":"2099-01-01T00:00:00Z"}}`))
			return
		}
		if r.URL.Path == "/forapi/v2/locations" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	var leaderErr, followerErr error
	var wg sync.WaitGroup

	// Leader
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, leaderErr = c.GetLocations(context.Background())
	}()

	<-fetchStarted

	// Follower
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(followerReady)
		_, followerErr = c.GetLocations(context.Background())
	}()

	<-followerReady
	// Give follower a moment to block on <-call.done
	time.Sleep(10 * time.Millisecond)

	// Explicitly invalidate while fetch is in-flight
	c.InvalidateToken()

	wg.Wait()

	if leaderErr != nil {
		t.Errorf("expected leader to succeed after invalidation retry, got: %v", leaderErr)
	}
	if followerErr != nil {
		t.Errorf("expected follower to succeed after invalidation retry, got: %v", followerErr)
	}

	tok, _ := c.CachedToken()
	if tok != "fresh-tok" {
		t.Errorf("expected cached token to be fresh-tok, got %q", tok)
	}
}

type panickingTransport struct {
	shouldPanic bool
	base        http.RoundTripper
}

func (p *panickingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if p.shouldPanic && strings.Contains(req.URL.Path, "access-token") {
		panic("simulated transport panic during token fetch")
	}
	return p.base.RoundTrip(req)
}

func TestTokenPanicDeadlockPrevention(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":{"token":"post-panic-tok","expiresAt":"2099-01-01T00:00:00Z"}}`))
			return
		}
		if r.URL.Path == "/forapi/v2/locations" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","data":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	transport := &panickingTransport{
		shouldPanic: true,
		base:        http.DefaultTransport,
	}
	httpClient := &http.Client{Transport: transport}

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m,
		client.WithHTTPClient(httpClient),
		client.WithInitialBackoff(1*time.Millisecond),
	)

	// Attempt 1 panics
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic during token fetch, got none")
			}
		}()
		_, _ = c.GetLocations(context.Background())
	}()

	// Subsequent request should not deadlock or hang
	transport.shouldPanic = false

	done := make(chan error, 1)
	go func() {
		_, err := c.GetLocations(context.Background())
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("subsequent call failed after panic recovery: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subsequent call deadlocked after panic in token fetch")
	}

	tok, _ := c.CachedToken()
	if tok != "post-panic-tok" {
		t.Errorf("expected cached token to be 'post-panic-tok', got %q", tok)
	}
}

func TestCloudflareHTMLChallengeDetection(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<!DOCTYPE html><html><head><title>Just a moment...</title></head><body>Cloudflare challenge platform</body></html>`))
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	_, err := c.GetLocations(context.Background())
	if err == nil {
		t.Fatal("expected error on HTML response, got nil")
	}
	if !strings.Contains(err.Error(), "Cloudflare challenge or WAF block") {
		t.Errorf("expected Cloudflare/WAF block mention in error, got: %v", err)
	}
}

func TestResponseBodyExceededLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Write 11MB of data
		chunk := make([]byte, 1024*1024)
		for i := 0; i < 11; i++ {
			_, _ = w.Write(chunk)
		}
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    5 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m,
		client.WithToken("valid-tok", time.Now().Add(1*time.Hour)),
		client.WithInitialBackoff(1*time.Millisecond),
	)

	_, err := c.GetLocations(context.Background())
	if err == nil {
		t.Fatal("expected error on body exceeding limit, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded") || !strings.Contains(err.Error(), "limit") {
		t.Errorf("expected body size limit error, got: %v", err)
	}
}

func TestEndpointURLResolution(t *testing.T) {
	cases := []struct {
		name     string
		baseURL  string
		subpath  string
		expected string
	}{
		{
			name:     "domain only",
			baseURL:  "https://bts.bcrs.sg",
			subpath:  "/locations",
			expected: "https://bts.bcrs.sg/forapi/v2/locations",
		},
		{
			name:     "domain with trailing slash",
			baseURL:  "https://bts.bcrs.sg/",
			subpath:  "/locations/access-token",
			expected: "https://bts.bcrs.sg/forapi/v2/locations/access-token",
		},
		{
			name:     "base includes /forapi/v2",
			baseURL:  "https://bts.bcrs.sg/forapi/v2",
			subpath:  "/locations",
			expected: "https://bts.bcrs.sg/forapi/v2/locations",
		},
		{
			name:     "base includes /forapi/v2 with trailing slash",
			baseURL:  "https://bts.bcrs.sg/forapi/v2/",
			subpath:  "/locations/rvms/1035/bin-status",
			expected: "https://bts.bcrs.sg/forapi/v2/locations/rvms/1035/bin-status",
		},
		{
			name:     "custom proxy path",
			baseURL:  "http://localhost:8080/custom/api",
			subpath:  "/locations",
			expected: "http://localhost:8080/custom/api/locations",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := client.New(&config.Config{BaseURL: tc.baseURL}, nil)
			got := c.EndpointURL(tc.subpath)
			if got != tc.expected {
				t.Errorf("EndpointURL(%q) with baseURL %q = %q, expected %q", tc.subpath, tc.baseURL, got, tc.expected)
			}
		})
	}
}

func TestForAPIV2RequestPaths(t *testing.T) {
	var requestedPaths []string
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestedPaths = append(requestedPaths, r.URL.Path)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/forapi/v2/locations/access-token" {
			_, _ = w.Write([]byte(`{"status":"ok","data":{"token":"test-tok","expiresAt":"2099-01-01T00:00:00Z"}}`))
			return
		}
		if r.URL.Path == "/forapi/v2/locations" {
			_, _ = w.Write([]byte(`{"status":"ok","data":[]}`))
			return
		}
		if r.URL.Path == "/forapi/v2/locations/rvms/1035/bin-status" {
			_, _ = w.Write([]byte(`{"status":"ok","data":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.Config{
		BaseURL:        ts.URL,
		HTTPTimeout:    2 * time.Second,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(1*time.Millisecond))

	_, err := c.GetLocations(context.Background())
	if err != nil {
		t.Fatalf("unexpected GetLocations error: %v", err)
	}

	_, err = c.GetBinStatus(context.Background(), 1035)
	if err != nil {
		t.Fatalf("unexpected GetBinStatus error: %v", err)
	}

	expectedPaths := []string{
		"/forapi/v2/locations/access-token",
		"/forapi/v2/locations",
		"/forapi/v2/locations/rvms/1035/bin-status",
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requestedPaths) != len(expectedPaths) {
		t.Fatalf("expected %d requests, got %d: %v", len(expectedPaths), len(requestedPaths), requestedPaths)
	}
	for i, expected := range expectedPaths {
		if requestedPaths[i] != expected {
			t.Errorf("request %d: expected path %q, got %q", i, expected, requestedPaths[i])
		}
	}
}
