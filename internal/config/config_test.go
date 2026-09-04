package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/TheDJVG/BCRSTracking/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	os.Clearenv()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.BaseURL != "https://bts.bcrs.sg" {
		t.Errorf("expected default BaseURL 'https://bts.bcrs.sg', got %s", cfg.BaseURL)
	}
	if cfg.LocationPollInterval != 15*time.Minute {
		t.Errorf("expected 15m, got %v", cfg.LocationPollInterval)
	}
	if cfg.BinPollInterval != 5*time.Minute {
		t.Errorf("expected 5m, got %v", cfg.BinPollInterval)
	}
	if cfg.BinWorkerConcurrency != 8 {
		t.Errorf("expected 8 workers, got %d", cfg.BinWorkerConcurrency)
	}
	if cfg.RateLimitRPS != 8.0 {
		t.Errorf("expected 8.0 rps, got %f", cfg.RateLimitRPS)
	}
	if cfg.RateLimitBurst != 16 {
		t.Errorf("expected 16 burst, got %d", cfg.RateLimitBurst)
	}
	if cfg.HTTPTimeout != 10*time.Second {
		t.Errorf("expected 10s timeout, got %v", cfg.HTTPTimeout)
	}
	if cfg.ClickHouseAddr != "localhost:9000" {
		t.Errorf("expected 'localhost:9000', got %s", cfg.ClickHouseAddr)
	}
	if cfg.ClickHouseDatabase != "bcrs" {
		t.Errorf("expected 'bcrs', got %s", cfg.ClickHouseDatabase)
	}
	if cfg.ClickHouseUser != "default" {
		t.Errorf("expected 'default', got %s", cfg.ClickHouseUser)
	}
	if cfg.ClickHousePassword != "" {
		t.Errorf("expected empty password, got %s", cfg.ClickHousePassword)
	}
	if cfg.ClickHouseBatchSize != 500 {
		t.Errorf("expected batch size 500, got %d", cfg.ClickHouseBatchSize)
	}
	if cfg.ClickHouseFlushInterval != 5*time.Second {
		t.Errorf("expected flush interval 5s, got %v", cfg.ClickHouseFlushInterval)
	}
	if cfg.MetricsPort != 9090 {
		t.Errorf("expected 9090 port, got %d", cfg.MetricsPort)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected log level 'info', got %s", cfg.LogLevel)
	}
}

func TestLoadCustomEnv(t *testing.T) {
	os.Clearenv()
	os.Setenv("BCRS_BASE_URL", "https://custom.bcrs.local")
	os.Setenv("LOCATION_POLL_INTERVAL", "30m")
	os.Setenv("BIN_POLL_INTERVAL", "10m")
	os.Setenv("BIN_WORKER_CONCURRENCY", "12")
	os.Setenv("RATE_LIMIT_RPS", "10.5")
	os.Setenv("RATE_LIMIT_BURST", "20")
	os.Setenv("HTTP_TIMEOUT", "15s")
	os.Setenv("CLICKHOUSE_ADDR", "clickhouse:9000")
	os.Setenv("CLICKHOUSE_DATABASE", "test_bcrs")
	os.Setenv("CLICKHOUSE_USER", "admin")
	os.Setenv("CLICKHOUSE_PASSWORD", "secret")
	os.Setenv("CLICKHOUSE_BATCH_SIZE", "1000")
	os.Setenv("CLICKHOUSE_FLUSH_INTERVAL", "2s")
	os.Setenv("METRICS_PORT", "9100")
	os.Setenv("LOG_LEVEL", "debug")
	defer os.Clearenv()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.BaseURL != "https://custom.bcrs.local" {
		t.Errorf("expected 'https://custom.bcrs.local', got %s", cfg.BaseURL)
	}
	if cfg.LocationPollInterval != 30*time.Minute {
		t.Errorf("expected 30m, got %v", cfg.LocationPollInterval)
	}
	if cfg.BinPollInterval != 10*time.Minute {
		t.Errorf("expected 10m, got %v", cfg.BinPollInterval)
	}
	if cfg.BinWorkerConcurrency != 12 {
		t.Errorf("expected 12, got %d", cfg.BinWorkerConcurrency)
	}
	if cfg.RateLimitRPS != 10.5 {
		t.Errorf("expected 10.5, got %f", cfg.RateLimitRPS)
	}
	if cfg.RateLimitBurst != 20 {
		t.Errorf("expected 20, got %d", cfg.RateLimitBurst)
	}
	if cfg.HTTPTimeout != 15*time.Second {
		t.Errorf("expected 15s, got %v", cfg.HTTPTimeout)
	}
	if cfg.ClickHouseAddr != "clickhouse:9000" {
		t.Errorf("expected 'clickhouse:9000', got %s", cfg.ClickHouseAddr)
	}
	if cfg.ClickHouseDatabase != "test_bcrs" {
		t.Errorf("expected 'test_bcrs', got %s", cfg.ClickHouseDatabase)
	}
	if cfg.ClickHouseUser != "admin" {
		t.Errorf("expected 'admin', got %s", cfg.ClickHouseUser)
	}
	if cfg.ClickHousePassword != "secret" {
		t.Errorf("expected 'secret', got %s", cfg.ClickHousePassword)
	}
	if cfg.ClickHouseBatchSize != 1000 {
		t.Errorf("expected 1000, got %d", cfg.ClickHouseBatchSize)
	}
	if cfg.ClickHouseFlushInterval != 2*time.Second {
		t.Errorf("expected 2s, got %v", cfg.ClickHouseFlushInterval)
	}
	if cfg.MetricsPort != 9100 {
		t.Errorf("expected 9100, got %d", cfg.MetricsPort)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected 'debug', got %s", cfg.LogLevel)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		envKey  string
		envVal  string
		wantErr string
	}{
		{
			name:    "invalid concurrency zero",
			envKey:  "BIN_WORKER_CONCURRENCY",
			envVal:  "0",
			wantErr: "BIN_WORKER_CONCURRENCY must be > 0",
		},
		{
			name:    "invalid concurrency negative",
			envKey:  "BIN_WORKER_CONCURRENCY",
			envVal:  "-5",
			wantErr: "BIN_WORKER_CONCURRENCY must be > 0",
		},
		{
			name:    "invalid rate limit rps zero",
			envKey:  "RATE_LIMIT_RPS",
			envVal:  "0",
			wantErr: "RATE_LIMIT_RPS must be > 0",
		},
		{
			name:    "invalid rate limit rps negative",
			envKey:  "RATE_LIMIT_RPS",
			envVal:  "-1.5",
			wantErr: "RATE_LIMIT_RPS must be > 0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			os.Clearenv()
			os.Setenv(tc.envKey, tc.envVal)
			defer os.Clearenv()

			cfg, err := config.Load()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil error and cfg: %+v", tc.wantErr, cfg)
			}
			if err.Error() != tc.wantErr {
				t.Errorf("expected error message %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}
