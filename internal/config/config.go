package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	BaseURL              string
	LocationPollInterval time.Duration
	BinPollInterval      time.Duration
	BinWorkerConcurrency int
	RateLimitRPS         float64
	RateLimitBurst       int
	HTTPTimeout          time.Duration

	ClickHouseAddr          string
	ClickHouseDatabase      string
	ClickHouseUser          string
	ClickHousePassword      string
	ClickHouseBatchSize     int
	ClickHouseFlushInterval time.Duration

	MetricsPort int
	LogLevel    string
}

func Load() (*Config, error) {
	cfg := &Config{
		BaseURL:                 getEnv("BCRS_BASE_URL", "https://bts.bcrs.sg"),
		LocationPollInterval:    getEnvDuration("LOCATION_POLL_INTERVAL", 15*time.Minute),
		BinPollInterval:         getEnvDuration("BIN_POLL_INTERVAL", 5*time.Minute),
		BinWorkerConcurrency:    getEnvInt("BIN_WORKER_CONCURRENCY", 8),
		RateLimitRPS:            getEnvFloat("RATE_LIMIT_RPS", 8.0),
		RateLimitBurst:          getEnvInt("RATE_LIMIT_BURST", 16),
		HTTPTimeout:             getEnvDuration("HTTP_TIMEOUT", 10*time.Second),
		ClickHouseAddr:          getEnv("CLICKHOUSE_ADDR", "localhost:9000"),
		ClickHouseDatabase:      getEnv("CLICKHOUSE_DATABASE", "bcrs"),
		ClickHouseUser:          getEnv("CLICKHOUSE_USER", "default"),
		ClickHousePassword:      getEnv("CLICKHOUSE_PASSWORD", ""),
		ClickHouseBatchSize:     getEnvInt("CLICKHOUSE_BATCH_SIZE", 500),
		ClickHouseFlushInterval: getEnvDuration("CLICKHOUSE_FLUSH_INTERVAL", 5*time.Second),
		MetricsPort:             getEnvInt("METRICS_PORT", 9090),
		LogLevel:                getEnv("LOG_LEVEL", "info"),
	}

	if cfg.BinWorkerConcurrency <= 0 {
		return nil, fmt.Errorf("BIN_WORKER_CONCURRENCY must be > 0")
	}
	if cfg.RateLimitRPS <= 0 {
		return nil, fmt.Errorf("RATE_LIMIT_RPS must be > 0")
	}
	return cfg, nil
}

func getEnv(key, def string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return def
}

func getEnvInt(key string, def int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return def
}

func getEnvFloat(key string, def float64) float64 {
	if val := os.Getenv(key); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return def
}
