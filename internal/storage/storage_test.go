package storage_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TheDJVG/BCRSTracking/internal/config"
	"github.com/TheDJVG/BCRSTracking/internal/metrics"
	"github.com/TheDJVG/BCRSTracking/internal/model"
	"github.com/TheDJVG/BCRSTracking/internal/storage"
	"github.com/google/uuid"
)

type mockSink struct {
	mu     sync.Mutex
	rvms   []model.RVM
	events []model.RVMEvent
	bins   []model.BinStatus
	err    error
}

func (m *mockSink) InsertRVMs(ctx context.Context, rvms []model.RVM) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.rvms = append(m.rvms, rvms...)
	return nil
}

func (m *mockSink) InsertEvents(ctx context.Context, events []model.RVMEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.events = append(m.events, events...)
	return nil
}

func (m *mockSink) InsertBins(ctx context.Context, bins []model.BinStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.bins = append(m.bins, bins...)
	return nil
}

func (m *mockSink) counts() (int, int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rvms), len(m.events), len(m.bins)
}

func (m *mockSink) getRVMs() []model.RVM {
	m.mu.Lock()
	defer m.mu.Unlock()
	dst := make([]model.RVM, len(m.rvms))
	copy(dst, m.rvms)
	return dst
}

func (m *mockSink) getEvents() []model.RVMEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	dst := make([]model.RVMEvent, len(m.events))
	copy(dst, m.events)
	return dst
}

func (m *mockSink) getBins() []model.BinStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	dst := make([]model.BinStatus, len(m.bins))
	copy(dst, m.bins)
	return dst
}

func (m *mockSink) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func TestBatchWriterBufferEnqueue(t *testing.T) {
	cfg := &config.Config{
		ClickHouseBatchSize:     10,
		ClickHouseFlushInterval: 100 * time.Millisecond,
	}
	m := metrics.New()
	sink := &mockSink{}
	bw := storage.NewBatchBuffer(cfg, m, sink)
	defer bw.Stop()

	rvm := model.RVM{ID: 1, SerialNumber: "S1", LocationName: "Loc 1", Status: "RUNNING"}
	evt := model.RVMEvent{EventID: uuid.New(), RVMID: 1, EventType: model.EventMachineAdded, RecordedAt: time.Now()}
	bin := model.BinStatus{ID: 1, RVMID: 1, CompartmentType: "CAN PET", CurrentCount: 10}

	bw.EnqueueRVM(rvm)
	bw.EnqueueEvent(evt)
	bw.EnqueueBin(bin)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	bw.Flush(ctx)

	rCount, eCount, bCount := sink.counts()
	if rCount != 1 || eCount != 1 || bCount != 1 {
		t.Fatalf("expected (1, 1, 1) flushed, got (%d, %d, %d)", rCount, eCount, bCount)
	}

	rvms := sink.getRVMs()
	if rvms[0].SerialNumber != "S1" {
		t.Errorf("expected serial 'S1', got '%s'", rvms[0].SerialNumber)
	}
}

func TestBatchWriterNilSink(t *testing.T) {
	cfg := &config.Config{
		ClickHouseBatchSize:     10,
		ClickHouseFlushInterval: 50 * time.Millisecond,
	}
	m := metrics.New()
	bw := storage.NewBatchBuffer(cfg, m, nil) // Nil sink should not panic

	rvm := model.RVM{ID: 1, SerialNumber: "S1"}
	bw.EnqueueRVM(rvm)
	bw.EnqueueEvent(model.RVMEvent{EventID: uuid.New(), RVMID: 1})
	bw.EnqueueBin(model.BinStatus{ID: 1, RVMID: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	bw.Flush(ctx)
	bw.Stop()
}

func TestBatchWriterBatchSizeTrigger(t *testing.T) {
	cfg := &config.Config{
		ClickHouseBatchSize:     3,
		ClickHouseFlushInterval: 10 * time.Second, // Long interval, should trigger on count
	}
	m := metrics.New()
	sink := &mockSink{}
	bw := storage.NewBatchBuffer(cfg, m, sink)
	defer bw.Stop()

	// Enqueue exactly 3 items to trigger batch
	for i := uint32(1); i <= 3; i++ {
		bw.EnqueueRVM(model.RVM{ID: i, SerialNumber: fmt.Sprintf("S%d", i)})
	}

	// Poll mock sink until batch size reached
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		rCount, _, _ := sink.counts()
		if rCount == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	rCount, _, _ := sink.counts()
	if rCount != 3 {
		t.Fatalf("expected batch size trigger of 3 RVMs, got %d", rCount)
	}
}

func TestBatchWriterTickerFlush(t *testing.T) {
	cfg := &config.Config{
		ClickHouseBatchSize:     100, // Large batch size
		ClickHouseFlushInterval: 50 * time.Millisecond,
	}
	m := metrics.New()
	sink := &mockSink{}
	bw := storage.NewBatchBuffer(cfg, m, sink)
	defer bw.Stop()

	bw.EnqueueRVM(model.RVM{ID: 100, SerialNumber: "TICK1"})
	bw.EnqueueEvent(model.RVMEvent{EventID: uuid.New(), RVMID: 100})
	bw.EnqueueBin(model.BinStatus{ID: 100, RVMID: 100})

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		rCount, eCount, bCount := sink.counts()
		if rCount == 1 && eCount == 1 && bCount == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	rCount, eCount, bCount := sink.counts()
	if rCount != 1 || eCount != 1 || bCount != 1 {
		t.Fatalf("expected ticker flush (1, 1, 1), got (%d, %d, %d)", rCount, eCount, bCount)
	}
}

func TestBatchWriterDrainOnStop(t *testing.T) {
	cfg := &config.Config{
		ClickHouseBatchSize:     1000,
		ClickHouseFlushInterval: 1 * time.Hour,
	}
	m := metrics.New()
	sink := &mockSink{}
	bw := storage.NewBatchBuffer(cfg, m, sink)

	// Enqueue items and immediately Stop
	for i := uint32(1); i <= 10; i++ {
		bw.EnqueueRVM(model.RVM{ID: i, SerialNumber: fmt.Sprintf("DRAIN_%d", i)})
		bw.EnqueueEvent(model.RVMEvent{EventID: uuid.New(), RVMID: i})
		bw.EnqueueBin(model.BinStatus{ID: uint64(i), RVMID: i})
	}

	bw.Stop()

	rCount, eCount, bCount := sink.counts()
	if rCount != 10 || eCount != 10 || bCount != 10 {
		t.Fatalf("expected drain on stop to flush (10, 10, 10), got (%d, %d, %d)", rCount, eCount, bCount)
	}
}

func TestBatchWriterConcurrent(t *testing.T) {
	cfg := &config.Config{
		ClickHouseBatchSize:     20,
		ClickHouseFlushInterval: 50 * time.Millisecond,
	}
	m := metrics.New()
	sink := &mockSink{}
	bw := storage.NewBatchBuffer(cfg, m, sink)

	var wg sync.WaitGroup
	goroutines := 10
	itemsPerGoroutine := 20

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < itemsPerGoroutine; i++ {
				id := uint32(gid*1000 + i)
				bw.EnqueueRVM(model.RVM{ID: id, SerialNumber: fmt.Sprintf("C_%d", id)})
				bw.EnqueueEvent(model.RVMEvent{EventID: uuid.New(), RVMID: id})
				bw.EnqueueBin(model.BinStatus{ID: uint64(id), RVMID: id})
			}
		}(g)
	}

	wg.Wait()
	bw.Stop()

	expected := goroutines * itemsPerGoroutine
	rCount, eCount, bCount := sink.counts()
	if rCount != expected || eCount != expected || bCount != expected {
		t.Fatalf("expected (%d, %d, %d), got (%d, %d, %d)", expected, expected, expected, rCount, eCount, bCount)
	}
}

func TestBatchWriterMetricsAndErrorHandling(t *testing.T) {
	cfg := &config.Config{
		ClickHouseBatchSize:     5,
		ClickHouseFlushInterval: 50 * time.Millisecond,
	}
	m := metrics.New()
	sink := &mockSink{}
	sink.setError(errors.New("clickhouse connection dropped"))

	bw := storage.NewBatchBuffer(cfg, m, sink)

	bw.EnqueueRVM(model.RVM{ID: 1, SerialNumber: "ERR1"})
	bw.EnqueueEvent(model.RVMEvent{EventID: uuid.New(), RVMID: 1})
	bw.EnqueueBin(model.BinStatus{ID: 1, RVMID: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	bw.Flush(ctx)

	// Now clear the error so retry on Stop succeeds
	sink.setError(nil)
	bw.Stop()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `bcrs_clickhouse_errors_total{table="rvm_current"} 1`) {
		t.Errorf("missing error metric for rvm_current: %s", body)
	}
	if !strings.Contains(body, `bcrs_clickhouse_errors_total{table="rvm_events"} 1`) {
		t.Errorf("missing error metric for rvm_events: %s", body)
	}
	if !strings.Contains(body, `bcrs_clickhouse_errors_total{table="rvm_bin_snapshots"} 1`) {
		t.Errorf("missing error metric for rvm_bin_snapshots: %s", body)
	}
	// After error cleared and stopped, rows should be inserted
	if !strings.Contains(body, `bcrs_clickhouse_rows_inserted_total{table="rvm_current"} 1`) {
		t.Errorf("missing retry rows inserted metric for rvm_current: %s", body)
	}
}

func TestSchemaDDLQueries(t *testing.T) {
	queries := storage.SchemaDDL()
	if len(queries) != 3 {
		t.Fatalf("expected 3 schema queries, got %d", len(queries))
	}

	// Verify rvm_current query
	if !strings.Contains(queries[0], "CREATE TABLE IF NOT EXISTS rvm_current") {
		t.Errorf("query 0 missing CREATE TABLE IF NOT EXISTS rvm_current: %s", queries[0])
	}
	if !strings.Contains(queries[0], "ReplacingMergeTree(updated_at)") {
		t.Errorf("query 0 missing ReplacingMergeTree(updated_at): %s", queries[0])
	}
	if !strings.Contains(queries[0], "raw_json String CODEC(ZSTD(3))") {
		t.Errorf("query 0 missing ZSTD(3) codec: %s", queries[0])
	}

	// Verify rvm_events query
	if !strings.Contains(queries[1], "CREATE TABLE IF NOT EXISTS rvm_events") {
		t.Errorf("query 1 missing CREATE TABLE IF NOT EXISTS rvm_events: %s", queries[1])
	}
	if !strings.Contains(queries[1], "PARTITION BY toYYYYMM(recorded_at)") {
		t.Errorf("query 1 missing partition by toYYYYMM: %s", queries[1])
	}
	if !strings.Contains(queries[1], "payload_diff String CODEC(ZSTD(3))") {
		t.Errorf("query 1 missing payload_diff ZSTD(3): %s", queries[1])
	}

	// Verify rvm_bin_snapshots query
	if !strings.Contains(queries[2], "CREATE TABLE IF NOT EXISTS rvm_bin_snapshots") {
		t.Errorf("query 2 missing CREATE TABLE IF NOT EXISTS rvm_bin_snapshots: %s", queries[2])
	}
	if !strings.Contains(queries[2], "TTL polled_at + INTERVAL 2 YEAR") {
		t.Errorf("query 2 missing TTL 2 YEAR: %s", queries[2])
	}
	if !strings.Contains(queries[2], "raw_json String CODEC(ZSTD(3))") {
		t.Errorf("query 2 missing raw_json ZSTD(3): %s", queries[2])
	}
}

func TestStorageDelegatesAndNewWithConn(t *testing.T) {
	cfg := &config.Config{
		ClickHouseBatchSize:     10,
		ClickHouseFlushInterval: 100 * time.Millisecond,
	}
	m := metrics.New()

	s := storage.NewWithConn(nil, cfg, m, nil)
	if s.Conn() != nil {
		t.Errorf("expected nil conn")
	}
	if s.Buffer() == nil {
		t.Fatalf("expected buffer to be initialized")
	}

	rvm := model.RVM{ID: 1, SerialNumber: "S_DEL", GroupIDs: []uint32{1, 2}}
	evt := model.RVMEvent{EventID: uuid.New(), RVMID: 1}
	bin := model.BinStatus{ID: 1, RVMID: 1}

	// Test delegation methods on Storage
	s.EnqueueRVM(rvm)
	s.EnqueueEvent(evt)
	s.EnqueueBin(bin)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s.Flush(ctx)

	if err := s.Close(); err != nil {
		t.Errorf("unexpected error on close: %v", err)
	}
}

func TestStorageEmptyInserts(t *testing.T) {
	s := storage.NewWithConn(nil, nil, nil, nil)
	defer s.Close()

	ctx := context.Background()
	if err := s.InsertRVMs(ctx, nil); err != nil {
		t.Errorf("expected nil for empty InsertRVMs, got %v", err)
	}
	if err := s.InsertEvents(ctx, nil); err != nil {
		t.Errorf("expected nil for empty InsertEvents, got %v", err)
	}
	if err := s.InsertBins(ctx, nil); err != nil {
		t.Errorf("expected nil for empty InsertBins, got %v", err)
	}
}
