package poller_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TheDJVG/BCRSTracking/internal/client"
	"github.com/TheDJVG/BCRSTracking/internal/config"
	"github.com/TheDJVG/BCRSTracking/internal/metrics"
	"github.com/TheDJVG/BCRSTracking/internal/model"
	"github.com/TheDJVG/BCRSTracking/internal/poller"
	"github.com/TheDJVG/BCRSTracking/internal/storage"
)

func TestStateDiffEngine(t *testing.T) {
	previous := map[uint32]model.RVM{
		101: {ID: 101, SerialNumber: "S1", SupplierID: "SUP1", Status: "RUNNING", LocationName: "Old Loc", RawJSON: `{"id":101}`},
		102: {ID: 102, SerialNumber: "S2", SupplierID: "SUP1", Status: "RUNNING", LocationName: "Loc 2", RawJSON: `{"id":102}`},
	}

	current := []model.RVM{
		{ID: 101, SerialNumber: "S1", SupplierID: "SUP1", Status: "FULL", LocationName: "Old Loc", RawJSON: `{"id":101,"status":"FULL"}`}, // Status changed
		{ID: 103, SerialNumber: "S3", SupplierID: "SUP2", Status: "RUNNING", LocationName: "Loc 3", RawJSON: `{"id":103}`},                // New machine
		// 102 is missing -> Removed
	}

	now := time.Now()
	diff := poller.DiffState(previous, current, now)

	if len(diff.Added) != 1 || diff.Added[0].ID != 103 {
		t.Fatalf("expected 1 added RVM (103), got %v", diff.Added)
	}
	if len(diff.Removed) != 1 || diff.Removed[0].ID != 102 {
		t.Fatalf("expected 1 removed RVM (102), got %v", diff.Removed)
	}
	if len(diff.Updated) != 1 || diff.Updated[0].ID != 101 {
		t.Fatalf("expected 1 updated RVM (101), got %v", diff.Updated)
	}
	if len(diff.Events) != 3 {
		// 1 added event + 1 status change event + 1 removed event
		t.Fatalf("expected 3 events, got %d", len(diff.Events))
	}

	// Verify added event
	var addedEvt, statusEvt, removedEvt *model.RVMEvent
	for i := range diff.Events {
		e := &diff.Events[i]
		switch e.EventType {
		case model.EventMachineAdded:
			addedEvt = e
		case model.EventStatusChanged:
			statusEvt = e
		case model.EventMachineRemoved:
			removedEvt = e
		}
	}

	if addedEvt == nil || addedEvt.RVMID != 103 || addedEvt.CurrentStatus != "RUNNING" {
		t.Errorf("unexpected added event: %+v", addedEvt)
	}
	if statusEvt == nil || statusEvt.RVMID != 101 || statusEvt.PreviousStatus != "RUNNING" || statusEvt.CurrentStatus != "FULL" {
		t.Errorf("unexpected status changed event: %+v", statusEvt)
	}
	if statusEvt != nil && statusEvt.PayloadDiff != `{"from":"RUNNING","to":"FULL"}` {
		t.Errorf("unexpected payload diff for status change: %s", statusEvt.PayloadDiff)
	}
	if removedEvt == nil || removedEvt.RVMID != 102 || removedEvt.CurrentStatus != "DELETED" {
		t.Errorf("unexpected removed event: %+v", removedEvt)
	}
	if removedEvt != nil && removedEvt.PayloadDiff != `{"deleted_id": 102}` {
		t.Errorf("unexpected payload diff for removed event: %s", removedEvt.PayloadDiff)
	}
}

func TestStateDiffEngine_MetadataUpdated(t *testing.T) {
	previous := map[uint32]model.RVM{
		201: {ID: 201, SerialNumber: "S201", SupplierID: "SUP1", Status: "RUNNING", LocationName: "Original Loc", Address: "Addr 1", RawJSON: `{"id":201}`},
	}

	current := []model.RVM{
		{ID: 201, SerialNumber: "S201", SupplierID: "SUP1", Status: "RUNNING", LocationName: "Renamed Loc", Address: "Addr 2", RawJSON: `{"id":201,"locationName":"Renamed Loc"}`},
	}

	now := time.Now()
	diff := poller.DiffState(previous, current, now)

	if len(diff.Added) != 0 {
		t.Errorf("expected 0 added, got %d", len(diff.Added))
	}
	if len(diff.Removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(diff.Removed))
	}
	if len(diff.Updated) != 1 || diff.Updated[0].ID != 201 {
		t.Fatalf("expected 1 updated RVM, got %v", diff.Updated)
	}
	if len(diff.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(diff.Events))
	}
	if diff.Events[0].EventType != model.EventMetadataUpdated {
		t.Errorf("expected EventMetadataUpdated, got %s", diff.Events[0].EventType)
	}
	if diff.Events[0].PayloadDiff != `{"id":201,"locationName":"Renamed Loc"}` {
		t.Errorf("unexpected payload diff: %s", diff.Events[0].PayloadDiff)
	}
}

func TestStateDiffEngine_NoChanges(t *testing.T) {
	previous := map[uint32]model.RVM{
		301: {ID: 301, SerialNumber: "S301", Status: "RUNNING", LocationName: "Loc A", Address: "Addr A"},
	}

	current := []model.RVM{
		{ID: 301, SerialNumber: "S301", Status: "RUNNING", LocationName: "Loc A", Address: "Addr A"},
	}

	diff := poller.DiffState(previous, current, time.Now())
	if len(diff.Added) != 0 || len(diff.Updated) != 0 || len(diff.Removed) != 0 || len(diff.Events) != 0 {
		t.Errorf("expected no diff results for identical state, got %+v", diff)
	}
}

func TestStateDiffEngine_Timestamps(t *testing.T) {
	firstSeen := time.Now().Add(-1 * time.Hour)
	previous := map[uint32]model.RVM{
		401: {ID: 401, SerialNumber: "S401", Status: "RUNNING", FirstSeenAt: firstSeen},
	}

	current := []model.RVM{
		{ID: 401, SerialNumber: "S401", Status: "FULL"},
		{ID: 402, SerialNumber: "S402", Status: "RUNNING"},
	}

	now := time.Now()
	diff := poller.DiffState(previous, current, now)

	if diff.Updated[0].FirstSeenAt != firstSeen {
		t.Errorf("expected FirstSeenAt %v preserved, got %v", firstSeen, diff.Updated[0].FirstSeenAt)
	}
	if diff.Updated[0].UpdatedAt != now {
		t.Errorf("expected UpdatedAt %v, got %v", now, diff.Updated[0].UpdatedAt)
	}

	if diff.Added[0].FirstSeenAt != now {
		t.Errorf("expected new RVM FirstSeenAt %v, got %v", now, diff.Added[0].FirstSeenAt)
	}
	if diff.Added[0].UpdatedAt != now {
		t.Errorf("expected new RVM UpdatedAt %v, got %v", now, diff.Added[0].UpdatedAt)
	}
}

func TestStateDiffEngine_ReAddedMachine(t *testing.T) {
	previous := map[uint32]model.RVM{
		501: {ID: 501, SerialNumber: "S501", Status: "DELETED", IsDeleted: 1},
	}

	current := []model.RVM{
		{ID: 501, SerialNumber: "S501", Status: "RUNNING", LocationName: "Reborn Loc"},
	}

	now := time.Now()
	diff := poller.DiffState(previous, current, now)

	if len(diff.Added) != 1 || diff.Added[0].ID != 501 {
		t.Fatalf("expected 1 added RVM (501), got %v", diff.Added)
	}
	if diff.Added[0].IsDeleted != 0 {
		t.Errorf("expected IsDeleted = 0, got %d", diff.Added[0].IsDeleted)
	}
	if len(diff.Removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(diff.Removed))
	}
	if len(diff.Events) != 1 || diff.Events[0].EventType != model.EventMachineAdded {
		t.Errorf("expected 1 EventMachineAdded event, got %+v", diff.Events)
	}
}

func TestPollLocations_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/locations" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := model.LocationsResponse{
			Status: "success",
			Data: []model.RVM{
				{ID: 1, SerialNumber: "M1", SupplierID: "SUP_A", Status: "RUNNING", LocationName: "Mall A"},
				{ID: 2, SerialNumber: "M2", SupplierID: "SUP_B", Status: "FULL", LocationName: "Mall B"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &config.Config{
		BaseURL:              server.URL,
		RateLimitRPS:         100,
		RateLimitBurst:       100,
		HTTPTimeout:          2 * time.Second,
		BinWorkerConcurrency: 2,
	}
	m := metrics.New()
	c := client.New(cfg, m)
	s := storage.NewWithConn(nil, cfg, m, nil)
	defer s.Close()

	p := poller.New(cfg, c, s, m, nil)
	ctx := context.Background()

	p.PollLocations(ctx)

	active := p.ActiveRVMs()
	if len(active) != 2 {
		t.Fatalf("expected 2 active RVMs, got %d", len(active))
	}
	if active[1].SerialNumber != "M1" || active[2].SerialNumber != "M2" {
		t.Errorf("unexpected active RVMs: %+v", active)
	}

	// Verify metrics
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)
	body := w.Body.String()

	if !strings.Contains(body, `bcrs_poll_cycle_total{result="success",type="locations"} 1`) {
		t.Errorf("missing success poll cycle metric: %s", body)
	}
	if !strings.Contains(body, `bcrs_rvm_total{coords_color="",status="RUNNING",supplier="SUP_A"} 1`) {
		t.Errorf("missing rvm_total metric for SUP_A: %s", body)
	}
	if !strings.Contains(body, `bcrs_rvm_total{coords_color="",status="FULL",supplier="SUP_B"} 1`) {
		t.Errorf("missing rvm_total metric for SUP_B: %s", body)
	}
}

func TestPollLocations_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server down", http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{
		BaseURL:              server.URL,
		RateLimitRPS:         100,
		RateLimitBurst:       100,
		HTTPTimeout:          2 * time.Second,
		BinWorkerConcurrency: 2,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(5*time.Millisecond))
	s := storage.NewWithConn(nil, cfg, m, nil)
	defer s.Close()

	p := poller.New(cfg, c, s, m, nil)
	ctx := context.Background()

	p.PollLocations(ctx)

	active := p.ActiveRVMs()
	if len(active) != 0 {
		t.Fatalf("expected 0 active RVMs on error, got %d", len(active))
	}

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)
	body := w.Body.String()

	if !strings.Contains(body, `bcrs_poll_cycle_total{result="error",type="locations"} 1`) {
		t.Errorf("missing error poll cycle metric: %s", body)
	}
}

func TestPollBins_WorkerPool(t *testing.T) {
	var requestedRVMs int32
	locs := []model.RVM{
		{ID: 1, SerialNumber: "S1", Status: "RUNNING"},
		{ID: 2, SerialNumber: "S2", Status: "RUNNING"},
		{ID: 3, SerialNumber: "S3", Status: "FULL"},
		{ID: 4, SerialNumber: "S4", Status: "RUNNING"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/locations" {
			resp := model.LocationsResponse{
				Status: "success",
				Data:   locs,
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/locations/rvms/") && strings.HasSuffix(r.URL.Path, "/bin-status") {
			atomic.AddInt32(&requestedRVMs, 1)
			resp := model.BinStatusResponse{
				Status: "success",
				Data: []model.BinStatus{
					{ID: 10, CompartmentType: "CAN PET", Capacity: 500, CurrentCount: 150, ThresholdLevel: 30},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := &config.Config{
		BaseURL:              server.URL,
		RateLimitRPS:         100,
		RateLimitBurst:       100,
		HTTPTimeout:          2 * time.Second,
		BinWorkerConcurrency: 4,
	}
	m := metrics.New()
	c := client.New(cfg, m)
	s := storage.NewWithConn(nil, cfg, m, nil)
	defer s.Close()

	p := poller.New(cfg, c, s, m, nil)

	ctx := context.Background()
	p.PollLocationsOnce(ctx)

	if len(p.ActiveRVMs()) != 4 {
		t.Fatalf("expected 4 active RVMs, got %d", len(p.ActiveRVMs()))
	}

	p.PollBinsOnce(ctx)

	if count := atomic.LoadInt32(&requestedRVMs); count != 4 {
		t.Errorf("expected 4 bin status requests, got %d", count)
	}

	// Verify metrics
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)
	body := w.Body.String()

	if !strings.Contains(body, `bcrs_poll_cycle_total{result="success",type="bins"} 1`) {
		t.Errorf("missing success poll cycle metric for bins: %s", body)
	}
}

func TestPollBins_PartialErrors(t *testing.T) {
	locs := []model.RVM{
		{ID: 1, SerialNumber: "S1", Status: "RUNNING"},
		{ID: 2, SerialNumber: "S2", Status: "RUNNING"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/locations" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(model.LocationsResponse{Status: "success", Data: locs})
			return
		}
		if r.URL.Path == "/api/v1/locations/rvms/1/bin-status" {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/api/v1/locations/rvms/2/bin-status" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(model.BinStatusResponse{
				Status: "success",
				Data: []model.BinStatus{
					{ID: 20, CompartmentType: "CAN PET", Capacity: 500, CurrentCount: 100},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := &config.Config{
		BaseURL:              server.URL,
		RateLimitRPS:         100,
		RateLimitBurst:       100,
		HTTPTimeout:          1 * time.Second,
		BinWorkerConcurrency: 2,
	}
	m := metrics.New()
	c := client.New(cfg, m, client.WithInitialBackoff(2*time.Millisecond))
	s := storage.NewWithConn(nil, cfg, m, nil)
	defer s.Close()

	p := poller.New(cfg, c, s, m, nil)
	ctx := context.Background()

	p.PollLocations(ctx)
	// PollBins should continue and not hang even if one RVM fails
	p.PollBins(ctx)
}

func TestPollBins_NoActiveRVMs(t *testing.T) {
	cfg := &config.Config{
		BinWorkerConcurrency: 2,
	}
	m := metrics.New()
	p := poller.New(cfg, nil, nil, m, nil)

	// Should return cleanly without panic or deadlock
	p.PollBins(context.Background())
}

func TestPollBins_ContextCancelled(t *testing.T) {
	locs := []model.RVM{
		{ID: 1, SerialNumber: "S1", Status: "RUNNING"},
		{ID: 2, SerialNumber: "S2", Status: "RUNNING"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/locations" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(model.LocationsResponse{Status: "success", Data: locs})
			return
		}
		// Artificial delay to allow cancellation during bin fetch
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.BinStatusResponse{Status: "success", Data: []model.BinStatus{}})
	}))
	defer server.Close()

	cfg := &config.Config{
		BaseURL:              server.URL,
		RateLimitRPS:         100,
		RateLimitBurst:       100,
		HTTPTimeout:          1 * time.Second,
		BinWorkerConcurrency: 2,
	}
	m := metrics.New()
	c := client.New(cfg, m)
	s := storage.NewWithConn(nil, cfg, m, nil)
	defer s.Close()

	p := poller.New(cfg, c, s, m, nil)
	p.PollLocations(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	p.PollBins(ctx)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)
	body := w.Body.String()

	if !strings.Contains(body, `bcrs_poll_cycle_total{result="error",type="bins"} 1`) {
		t.Errorf("missing error poll cycle metric for cancelled bins: %s", body)
	}
}

func TestPollLocations_GaugeResetOnStatusTransition(t *testing.T) {
	currentStatus := "RUNNING"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/locations" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := model.LocationsResponse{
			Status: "success",
			Data: []model.RVM{
				{ID: 1, SerialNumber: "M1", SupplierID: "SUP_A", Status: currentStatus},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &config.Config{
		BaseURL:              server.URL,
		RateLimitRPS:         100,
		RateLimitBurst:       100,
		HTTPTimeout:          1 * time.Second,
		BinWorkerConcurrency: 2,
	}
	m := metrics.New()
	c := client.New(cfg, m)
	s := storage.NewWithConn(nil, cfg, m, nil)
	defer s.Close()

	p := poller.New(cfg, c, s, m, nil)
	ctx := context.Background()

	// First poll: status is RUNNING
	p.PollLocations(ctx)

	req1 := httptest.NewRequest("GET", "/metrics", nil)
	w1 := httptest.NewRecorder()
	m.Handler().ServeHTTP(w1, req1)
	body1 := w1.Body.String()

	if !strings.Contains(body1, `bcrs_rvm_total{coords_color="",status="RUNNING",supplier="SUP_A"} 1`) {
		t.Fatalf("expected RUNNING status gauge in cycle 1: %s", body1)
	}

	// Second poll: status transitions to FULL
	currentStatus = "FULL"
	p.PollLocations(ctx)

	req2 := httptest.NewRequest("GET", "/metrics", nil)
	w2 := httptest.NewRecorder()
	m.Handler().ServeHTTP(w2, req2)
	body2 := w2.Body.String()

	if !strings.Contains(body2, `bcrs_rvm_total{coords_color="",status="FULL",supplier="SUP_A"} 1`) {
		t.Errorf("expected FULL status gauge in cycle 2: %s", body2)
	}
	if strings.Contains(body2, `status="RUNNING"`) {
		t.Errorf("expected stale RUNNING status gauge to be cleared after Reset, but found: %s", body2)
	}
}

func TestPoller_StartLifecycle(t *testing.T) {
	locPollCount := int32(0)
	binPollCount := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/locations" {
			atomic.AddInt32(&locPollCount, 1)
			resp := model.LocationsResponse{
				Status: "success",
				Data: []model.RVM{
					{ID: 1, SerialNumber: "S1", Status: "RUNNING"},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/locations/rvms/") {
			atomic.AddInt32(&binPollCount, 1)
			resp := model.BinStatusResponse{
				Status: "success",
				Data: []model.BinStatus{
					{ID: 1, CompartmentType: "CAN PET", Capacity: 500, CurrentCount: 100},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := &config.Config{
		BaseURL:              server.URL,
		LocationPollInterval: 25 * time.Millisecond,
		BinPollInterval:      25 * time.Millisecond,
		BinWorkerConcurrency: 2,
		RateLimitRPS:         100,
		RateLimitBurst:       100,
		HTTPTimeout:          1 * time.Second,
	}
	m := metrics.New()
	c := client.New(cfg, m)
	s := storage.NewWithConn(nil, cfg, m, nil)
	defer s.Close()

	p := poller.New(cfg, c, s, m, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Run Start blocking in goroutine
	done := make(chan struct{})
	go func() {
		p.Start(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Succeeded in cleanly shutting down on ctx cancel
	case <-time.After(2 * time.Second):
		t.Fatal("poller Start did not terminate within timeout after context cancellation")
	}

	if atomic.LoadInt32(&locPollCount) == 0 {
		t.Errorf("expected at least 1 location poll, got %d", atomic.LoadInt32(&locPollCount))
	}
	if atomic.LoadInt32(&binPollCount) == 0 {
		t.Errorf("expected at least 1 bin poll, got %d", atomic.LoadInt32(&binPollCount))
	}
}

func TestContainerIngestionTracking(t *testing.T) {
	var currentCount uint32 = 100
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/locations" {
			resp := model.LocationsResponse{
				Status: "success",
				Data: []model.RVM{
					{ID: 1, SerialNumber: "SG001", SupplierID: "TOMRA001", Status: "RUNNING"},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		if r.URL.Path == "/api/v1/locations/rvms/1/bin-status" {
			resp := model.BinStatusResponse{
				Status: "success",
				Data: []model.BinStatus{
					{ID: 101, RVMID: 1, CompartmentType: "PLASTIC", Capacity: 500, CurrentCount: atomic.LoadUint32(&currentCount)},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := &config.Config{
		BaseURL:              server.URL,
		LocationPollInterval: 10 * time.Minute,
		BinPollInterval:      10 * time.Minute,
		BinWorkerConcurrency: 2,
		RateLimitRPS:         100,
		RateLimitBurst:       100,
		HTTPTimeout:          1 * time.Second,
	}
	m := metrics.New()
	c := client.New(cfg, m)
	s := storage.NewWithConn(nil, cfg, m, nil)
	defer s.Close()

	p := poller.New(cfg, c, s, m, nil)
	ctx := context.Background()

	// Initial poll: locations then bins
	p.PollLocations(ctx)
	p.PollBins(ctx)

	// Ingestion: count increased from 100 to 135 (+35)
	atomic.StoreUint32(&currentCount, 135)
	p.PollBins(ctx)

	// Sensor jitter / noise: count drops slightly from 135 to 133 (delta should be 0, not 133!)
	atomic.StoreUint32(&currentCount, 133)
	p.PollBins(ctx)

	// Emptied: count drops from 133 to 12 (prevCount >= 50 and 12 <= 133/2 -> +12 newly ingested)
	atomic.StoreUint32(&currentCount, 12)
	p.PollBins(ctx)

	// Check metrics output
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)
	body := w.Body.String()

	// Total ingested should be 35 + 0 + 12 = 47
	expectedIngested := `bcrs_containers_ingested_total{compartment_type="PLASTIC",supplier="TOMRA001"} 47`
	if !strings.Contains(body, expectedIngested) {
		t.Errorf("expected metrics to contain '%s', got body:\n%s", expectedIngested, body)
	}

	expectedCurrent := `bcrs_bin_current_count{compartment_type="PLASTIC",supplier="TOMRA001"} 12`
	if !strings.Contains(body, expectedCurrent) {
		t.Errorf("expected metrics to contain '%s', got body:\n%s", expectedCurrent, body)
	}
}

func TestPoller_HydrateFromStorage_GracefulFallback(t *testing.T) {
	cfg := &config.Config{}
	m := metrics.New()
	p := poller.New(cfg, nil, nil, m, nil)

	// Hydrate with nil storage should return cleanly without error
	err := p.HydrateFromStorage(context.Background())
	if err != nil {
		t.Errorf("expected nil error on nil storage hydration, got %v", err)
	}
	if len(p.ActiveRVMs()) != 0 {
		t.Errorf("expected empty activeRVMs, got %d", len(p.ActiveRVMs()))
	}

	// Storage with nil connection should log warning and return cleanly without panic
	s := storage.NewWithConn(nil, cfg, m, nil)
	p2 := poller.New(cfg, nil, s, m, nil)
	err = p2.HydrateFromStorage(context.Background())
	if err != nil {
		t.Errorf("expected nil error on failed storage hydration, got %v", err)
	}
	if len(p2.ActiveRVMs()) != 0 {
		t.Errorf("expected empty activeRVMs on fallback, got %d", len(p2.ActiveRVMs()))
	}
}

func TestPoller_ApplyHydratedBinSnapshots_HappyPath(t *testing.T) {
	cfg := &config.Config{}
	m := metrics.New()
	p := poller.New(cfg, nil, nil, m, nil)

	// Set active RVMs in poller so suppliers are resolved
	rvms := []model.RVM{
		{ID: 1, SupplierID: "RVMS001", Status: "RUNNING"},
		{ID: 2, SupplierID: "SGRECYCLE001", Status: "RUNNING"},
		{ID: 3, SupplierID: "TOMRA001", Status: "RUNNING"},
	}
	p.SetActiveRVMsForTest(rvms)

	// Snapshot list with multiple compartment types and vendor variations
	snapshots := []storage.LatestBinInfo{
		{RVMID: 1, SerialNumber: "RVM01", CompartmentType: "CAN PET", Capacity: 1500, CurrentCount: 450, ThresholdLevel: 30},
		{RVMID: 2, SerialNumber: "SGR01", CompartmentType: "Bottle Can", Capacity: 800, CurrentCount: 220, ThresholdLevel: 27},
		{RVMID: 3, SerialNumber: "TOM01", CompartmentType: "CAN PET", Capacity: 0, CurrentCount: 50000, ThresholdLevel: 45}, // 45% * 10 = 450 in-bin
	}

	p.ApplyHydratedBinSnapshotsForTest(snapshots)

	// Verify Prometheus metrics output
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)
	body := w.Body.String()

	// RVMS001 in-bin count should be 450
	if !strings.Contains(body, `bcrs_bin_current_count{compartment_type="CAN PET",supplier="RVMS001"} 450`) {
		t.Errorf("expected RVMS001 in-bin count 450 in metrics, got:\n%s", body)
	}

	// SGRECYCLE001 in-bin count should be 220
	if !strings.Contains(body, `bcrs_bin_current_count{compartment_type="Bottle Can",supplier="SGRECYCLE001"} 220`) {
		t.Errorf("expected SGRECYCLE001 in-bin count 220 in metrics, got:\n%s", body)
	}

	// TOMRA001 in-bin count should be 450 (45% of 1000)
	if !strings.Contains(body, `bcrs_bin_current_count{compartment_type="CAN PET",supplier="TOMRA001"} 450`) {
		t.Errorf("expected TOMRA001 in-bin count 450 in metrics, got:\n%s", body)
	}
}
