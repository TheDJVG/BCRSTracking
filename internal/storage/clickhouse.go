package storage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/TheDJVG/BCRSTracking/internal/config"
	"github.com/TheDJVG/BCRSTracking/internal/metrics"
	"github.com/TheDJVG/BCRSTracking/internal/model"
)

// SchemaDDL returns the DDL statements for the ClickHouse schema.
func SchemaDDL() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS rvm_current (
			id UInt32,
			serial_number LowCardinality(String),
			supplier_id LowCardinality(String),
			location_name String,
			latitude Float64,
			longitude Float64,
			coords_color LowCardinality(String),
			address String,
			postal_code LowCardinality(String),
			status LowCardinality(String),
			rvm_remarks String,
			model LowCardinality(String),
			rvm_type LowCardinality(String),
			group_ids Array(UInt32),
			opening_hours String,
			raw_json String CODEC(ZSTD(3)),
			first_seen_at DateTime64(3, 'UTC'),
			updated_at DateTime64(3, 'UTC'),
			is_deleted UInt8 DEFAULT 0
		) ENGINE = ReplacingMergeTree(updated_at)
		PRIMARY KEY (id)
		ORDER BY (id);`,

		`CREATE TABLE IF NOT EXISTS rvm_events (
			event_id UUID,
			rvm_id UInt32,
			serial_number LowCardinality(String),
			supplier_id LowCardinality(String),
			event_type LowCardinality(String),
			previous_status LowCardinality(String),
			current_status LowCardinality(String),
			payload_diff String CODEC(ZSTD(3)),
			recorded_at DateTime64(3, 'UTC')
		) ENGINE = MergeTree()
		PARTITION BY toYYYYMM(recorded_at)
		ORDER BY (recorded_at, rvm_id, event_type);`,

		`CREATE TABLE IF NOT EXISTS rvm_bin_snapshots (
			bin_record_id UInt64,
			rvm_id UInt32,
			serial_number LowCardinality(String),
			compartment_type LowCardinality(String),
			capacity UInt32,
			current_count UInt32,
			threshold_level UInt8,
			upstream_last_updated Nullable(DateTime64(3, 'UTC')),
			polled_at DateTime64(3, 'UTC'),
			raw_json String CODEC(ZSTD(3))
		) ENGINE = MergeTree()
		PARTITION BY toYYYYMM(polled_at)
		ORDER BY (compartment_type, rvm_id, polled_at)
		TTL polled_at + INTERVAL 2 YEAR;`,
	}
}

type Storage struct {
	conn   driver.Conn
	buffer *BatchBuffer
	logger *slog.Logger
}

func New(ctx context.Context, cfg *config.Config, m *metrics.Metrics, logger *slog.Logger) (*Storage, error) {
	if logger == nil {
		logger = slog.Default()
	}
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{cfg.ClickHouseAddr},
		Auth: clickhouse.Auth{
			Database: cfg.ClickHouseDatabase,
			Username: cfg.ClickHouseUser,
			Password: cfg.ClickHousePassword,
		},
		DialTimeout:     5 * time.Second,
		MaxOpenConns:    20,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ClickHouse: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ClickHouse ping failed: %w", err)
	}

	s := &Storage{
		conn:   conn,
		logger: logger,
	}

	s.buffer = NewBatchBuffer(cfg, m, s)
	return s, nil
}

func NewWithConn(conn driver.Conn, cfg *config.Config, m *metrics.Metrics, logger *slog.Logger) *Storage {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Storage{
		conn:   conn,
		logger: logger,
	}
	s.buffer = NewBatchBuffer(cfg, m, s)
	return s
}

func (s *Storage) InitSchema(ctx context.Context) error {
	if s.conn == nil {
		return fmt.Errorf("clickhouse connection is nil")
	}
	queries := SchemaDDL()
	for _, q := range queries {
		if err := s.conn.Exec(ctx, q); err != nil {
			return fmt.Errorf("failed executing schema query: %w", err)
		}
	}
	return nil
}

func (s *Storage) InsertRVMs(ctx context.Context, rvms []model.RVM) error {
	if len(rvms) == 0 {
		return nil
	}
	if s.conn == nil {
		return fmt.Errorf("clickhouse connection is nil")
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO rvm_current (id, serial_number, supplier_id, location_name, latitude, longitude, coords_color, address, postal_code, status, rvm_remarks, model, rvm_type, group_ids, opening_hours, raw_json, first_seen_at, updated_at, is_deleted)")
	if err != nil {
		return fmt.Errorf("failed to prepare rvm_current batch: %w", err)
	}
	for _, r := range rvms {
		groupIDs := r.GroupIDs
		if groupIDs == nil {
			groupIDs = []uint32{}
		}
		err := batch.Append(
			r.ID, r.SerialNumber, r.SupplierID, r.LocationName,
			r.Latitude, r.Longitude, r.CoordsColor, r.Address,
			r.PostalCode, r.Status, r.RVMRemarks, r.Model,
			r.RVMType, groupIDs, r.OpeningHours, r.RawJSON,
			r.FirstSeenAt, r.UpdatedAt, r.IsDeleted,
		)
		if err != nil {
			return fmt.Errorf("failed appending rvm row: %w", err)
		}
	}
	return batch.Send()
}

func (s *Storage) InsertEvents(ctx context.Context, events []model.RVMEvent) error {
	if len(events) == 0 {
		return nil
	}
	if s.conn == nil {
		return fmt.Errorf("clickhouse connection is nil")
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO rvm_events (event_id, rvm_id, serial_number, supplier_id, event_type, previous_status, current_status, payload_diff, recorded_at)")
	if err != nil {
		return fmt.Errorf("failed to prepare rvm_events batch: %w", err)
	}
	for _, e := range events {
		err := batch.Append(
			e.EventID, e.RVMID, e.SerialNumber, e.SupplierID,
			string(e.EventType), e.PreviousStatus, e.CurrentStatus,
			e.PayloadDiff, e.RecordedAt,
		)
		if err != nil {
			return fmt.Errorf("failed appending event row: %w", err)
		}
	}
	return batch.Send()
}

func (s *Storage) InsertBins(ctx context.Context, bins []model.BinStatus) error {
	if len(bins) == 0 {
		return nil
	}
	if s.conn == nil {
		return fmt.Errorf("clickhouse connection is nil")
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO rvm_bin_snapshots (bin_record_id, rvm_id, serial_number, compartment_type, capacity, current_count, threshold_level, upstream_last_updated, polled_at, raw_json)")
	if err != nil {
		return fmt.Errorf("failed to prepare rvm_bin_snapshots batch: %w", err)
	}
	for _, b := range bins {
		err := batch.Append(
			b.ID, b.RVMID, b.SerialNumber, b.CompartmentType,
			b.Capacity, b.CurrentCount, b.ThresholdLevel,
			b.LastUpdated, b.PolledAt, b.RawJSON,
		)
		if err != nil {
			return fmt.Errorf("failed appending bin row: %w", err)
		}
	}
	return batch.Send()
}

func (s *Storage) EnqueueRVM(r model.RVM) {
	if s.buffer != nil {
		s.buffer.EnqueueRVM(r)
	}
}

func (s *Storage) EnqueueEvent(e model.RVMEvent) {
	if s.buffer != nil {
		s.buffer.EnqueueEvent(e)
	}
}

func (s *Storage) EnqueueBin(b model.BinStatus) {
	if s.buffer != nil {
		s.buffer.EnqueueBin(b)
	}
}

func (s *Storage) Flush(ctx context.Context) {
	if s.buffer != nil {
		s.buffer.Flush(ctx)
	}
}

func (s *Storage) Buffer() *BatchBuffer {
	return s.buffer
}

func (s *Storage) Conn() driver.Conn {
	return s.conn
}

func (s *Storage) LoadActiveRVMs(ctx context.Context) (map[uint32]model.RVM, error) {
	if s.conn == nil {
		return nil, fmt.Errorf("clickhouse connection is nil")
	}

	rows, err := s.conn.Query(ctx, `
		SELECT
			id,
			serial_number,
			supplier_id,
			location_name,
			latitude,
			longitude,
			coords_color,
			address,
			postal_code,
			status,
			rvm_remarks,
			model,
			rvm_type,
			group_ids,
			opening_hours,
			raw_json,
			first_seen_at,
			updated_at,
			is_deleted
		FROM rvm_current FINAL
		WHERE is_deleted = 0
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query active RVMs from ClickHouse: %w", err)
	}
	defer rows.Close()

	active := make(map[uint32]model.RVM)
	for rows.Next() {
		var r model.RVM
		var groupIDs []uint32
		if err := rows.Scan(
			&r.ID,
			&r.SerialNumber,
			&r.SupplierID,
			&r.LocationName,
			&r.Latitude,
			&r.Longitude,
			&r.CoordsColor,
			&r.Address,
			&r.PostalCode,
			&r.Status,
			&r.RVMRemarks,
			&r.Model,
			&r.RVMType,
			&groupIDs,
			&r.OpeningHours,
			&r.RawJSON,
			&r.FirstSeenAt,
			&r.UpdatedAt,
			&r.IsDeleted,
		); err != nil {
			return nil, fmt.Errorf("failed to scan rvm_current row: %w", err)
		}
		r.GroupIDs = groupIDs
		active[r.ID] = r
	}
	return active, rows.Err()
}

type LatestBinInfo struct {
	RVMID           uint32
	SerialNumber    string
	CompartmentType string
	Capacity        uint32
	CurrentCount    uint32
	ThresholdLevel  uint8
}

func (s *Storage) LoadLatestBinSnapshots(ctx context.Context) ([]LatestBinInfo, error) {
	if s.conn == nil {
		return nil, fmt.Errorf("clickhouse connection is nil")
	}

	rows, err := s.conn.Query(ctx, `
		SELECT
			rvm_id,
			argMax(serial_number, polled_at) AS serial_number,
			compartment_type,
			argMax(capacity, polled_at) AS capacity,
			argMax(current_count, polled_at) AS latest_count,
			argMax(threshold_level, polled_at) AS threshold_level
		FROM rvm_bin_snapshots
		GROUP BY rvm_id, compartment_type
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query latest bin snapshots from ClickHouse: %w", err)
	}
	defer rows.Close()

	var list []LatestBinInfo
	for rows.Next() {
		var info LatestBinInfo
		if err := rows.Scan(
			&info.RVMID,
			&info.SerialNumber,
			&info.CompartmentType,
			&info.Capacity,
			&info.CurrentCount,
			&info.ThresholdLevel,
		); err != nil {
			return nil, fmt.Errorf("failed to scan bin snapshot row: %w", err)
		}
		list = append(list, info)
	}
	return list, rows.Err()
}

func (s *Storage) Close() error {
	if s.buffer != nil {
		s.buffer.Stop()
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}
