package metrics_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TheDJVG/BCRSTracking/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestMetricsEndpoints(t *testing.T) {
	m := metrics.New()

	m.RecordHTTPRequest("/locations", 200, 150*time.Millisecond)
	m.RecordRateLimitBackoff("/locations/rvms/213/bin-status")
	m.RecordAuthError("/locations")
	m.SetRVMTotal("RUNNING", "SGRECYCLE001", "Red", 42)
	m.RecordContainersIngested("PLASTIC", "TOMRA001", 15)
	m.SetBinCurrentCount("PLASTIC", "TOMRA001", 120)
	m.RecordClickHouseInsert("rvm_bin_snapshots", 100, 50*time.Millisecond, nil)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	handler := promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "bcrs_http_requests_total") {
		t.Errorf("missing bcrs_http_requests_total in metrics output")
	}
	if !strings.Contains(body, "bcrs_rvm_total") {
		t.Errorf("missing bcrs_rvm_total in metrics output")
	}
	if !strings.Contains(body, "bcrs_rate_limit_backoffs_total") {
		t.Errorf("missing bcrs_rate_limit_backoffs_total in metrics output")
	}
	if !strings.Contains(body, "bcrs_auth_errors_total") {
		t.Errorf("missing bcrs_auth_errors_total in metrics output")
	}
	if !strings.Contains(body, "bcrs_containers_ingested_total") {
		t.Errorf("missing bcrs_containers_ingested_total in metrics output")
	}
	if !strings.Contains(body, "bcrs_bin_current_count") {
		t.Errorf("missing bcrs_bin_current_count in metrics output")
	}
	if !strings.Contains(body, "bcrs_clickhouse_rows_inserted_total") {
		t.Errorf("missing bcrs_clickhouse_rows_inserted_total in metrics output")
	}
}

func TestHandler(t *testing.T) {
	m := metrics.New()
	m.RecordHTTPRequest("/locations", 200, 100*time.Millisecond)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	handler := m.Handler()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "bcrs_http_requests_total") {
		t.Errorf("missing bcrs_http_requests_total in Handler output")
	}
}

func TestRecordPollCycle(t *testing.T) {
	m := metrics.New()

	// Test success poll cycle
	m.RecordPollCycle("full_sync", 2*time.Second, nil)

	// Test error poll cycle
	m.RecordPollCycle("bin_status", 500*time.Millisecond, errors.New("timeout"))

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `bcrs_poll_cycle_total{result="success",type="full_sync"} 1`) {
		t.Errorf("missing success poll cycle count in metrics output: %s", body)
	}
	if !strings.Contains(body, `bcrs_poll_cycle_total{result="error",type="bin_status"} 1`) {
		t.Errorf("missing error poll cycle count in metrics output: %s", body)
	}
	if !strings.Contains(body, "bcrs_poll_cycle_duration_seconds") {
		t.Errorf("missing bcrs_poll_cycle_duration_seconds in metrics output")
	}
	if !strings.Contains(body, "bcrs_poll_cycle_last_duration_seconds") {
		t.Errorf("missing bcrs_poll_cycle_last_duration_seconds in metrics output")
	}
	if !strings.Contains(body, "bcrs_poll_cycle_last_success_timestamp_seconds") {
		t.Errorf("missing bcrs_poll_cycle_last_success_timestamp_seconds in metrics output")
	}
}

func TestRecordClickHouseInsert(t *testing.T) {
	m := metrics.New()

	// Successful insert
	m.RecordClickHouseInsert("rvm_locations", 50, 25*time.Millisecond, nil)

	// Failed insert
	m.RecordClickHouseInsert("rvm_locations", 0, 10*time.Millisecond, errors.New("connection refused"))

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `bcrs_clickhouse_rows_inserted_total{table="rvm_locations"} 50`) {
		t.Errorf("missing rows inserted metric: %s", body)
	}
	if !strings.Contains(body, `bcrs_clickhouse_errors_total{table="rvm_locations"} 1`) {
		t.Errorf("missing error count metric: %s", body)
	}
	if !strings.Contains(body, "bcrs_clickhouse_insert_duration_seconds") {
		t.Errorf("missing insert duration metric")
	}
}

func TestSetClickHouseBufferDepth(t *testing.T) {
	m := metrics.New()
	m.SetClickHouseBufferDepth("rvm_current", 123)
	m.SetClickHouseBufferDepth("rvm_events", 45)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `bcrs_clickhouse_buffer_depth{table="rvm_current"} 123`) {
		t.Errorf("missing buffer depth metric for rvm_current: %s", body)
	}
	if !strings.Contains(body, `bcrs_clickhouse_buffer_depth{table="rvm_events"} 45`) {
		t.Errorf("missing buffer depth metric for rvm_events: %s", body)
	}
}

func TestConcurrentRecording(t *testing.T) {
	m := metrics.New()
	var wg sync.WaitGroup

	numGoroutines := 20
	iterations := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				m.RecordHTTPRequest("/locations", 200, time.Duration(j)*time.Millisecond)
				m.RecordRateLimitBackoff("/locations")
				m.RecordAuthError("/locations")
				m.SetRVMTotal("RUNNING", "SUPP", "Green", float64(j))
				m.RecordPollCycle("sync", 100*time.Millisecond, nil)
				m.RecordClickHouseInsert("table", 10, 5*time.Millisecond, nil)
			}
		}(i)
	}

	wg.Wait()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
