package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TheDJVG/BCRSTracking/internal/client"
	"github.com/TheDJVG/BCRSTracking/internal/config"
	"github.com/TheDJVG/BCRSTracking/internal/metrics"
)

func TestGetLocationsSuccess(t *testing.T) {
	var capturedUserAgent string
	var capturedAccept string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/locations" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		capturedUserAgent = r.Header.Get("User-Agent")
		capturedAccept = r.Header.Get("Accept")

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
}

func TestGetBinStatusSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/locations/rvms/202/bin-status" {
			http.NotFound(w, r)
			return
		}
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
}

func TestGetBinStatusWith429Retry(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","data":[{"id":999,"rvm_id":101,"compartment_type":"Bottle Can","capacity":800,"current_count":120,"threshold_level":15,"last_updated":"2026-08-23T05:00:00Z"}]}`))
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
		atomic.AddInt32(&attempts, 1)
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
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
		atomic.AddInt32(&attempts, 1)
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
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
		atomic.AddInt32(&attempts, 1)
		http.Error(w, "Not Found", http.StatusNotFound)
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
