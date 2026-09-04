# BCRS Singapore Bottle Return Tracking Application

A high-performance, cloud-native Go tracking service that monitors reverse vending machine (RVM) availability, status changes, bin capacity levels, and bottle return deposit trends across Singapore's Beverage Container Return Scheme (BCRS) network (`bts.bcrs.sg`).

[![Go Version](https://img.shields.io/badge/go-1.27-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![CI/CD](https://img.shields.io/badge/CI%2FCD-GitLab%20CI%20%2B%20Kaniko-orange.svg)](.gitlab-ci.yml)
[![Renovate](https://img.shields.io/badge/renovate-enabled-brightgreen.svg)](renovate.json)

---

## Table of Contents

- [Architecture & Overview](#architecture--overview)
- [Key Features](#key-features)
- [Storage Architecture & ClickHouse Schema](#storage-architecture--clickhouse-schema)
  - [Zero-Loss Schema Evolution](#zero-loss-schema-evolution)
  - [Table Schemas](#table-schemas)
  - [Ad-Hoc JSON Queries](#ad-hoc-json-queries)
- [Configuration Reference](#configuration-reference)
- [Observability & Metrics](#observability--metrics)
  - [Prometheus Metrics List](#prometheus-metrics-list)
  - [Sample PromQL Queries](#sample-promql-queries)
  - [Alerting Rules (VMRule)](#alerting-rules-vmrule)
  - [Grafana Dashboard](#grafana-dashboard)
- [Local Development Guide](#local-development-guide)
  - [Quickstart (Docker Compose)](#quickstart-docker-compose)
  - [Make Targets](#make-targets)
- [Production Deployment Guide (Kubernetes / Helm)](#production-deployment-guide-kubernetes--helm)
  - [Helm Installation](#helm-installation)
  - [Chart Features](#chart-features)
- [CI/CD Pipeline](#cicd-pipeline)

---

## Architecture & Overview

The BCRS Tracking Application continuously scrapes public upstream endpoints (`https://bts.bcrs.sg/api/v1/...`), computes differential state changes (machine added, updated, removed, status transitions), dispatches concurrent bin telemetry scrapes with token-bucket rate limiting, and batches writes to ClickHouse.

```
                   ┌───────────────────────────────────────────────┐
                   │           Upstream BCRS Platform              │
                   │             (bts.bcrs.sg/api/v1)              │
                   └──────────────────────┬────────────────────────┘
                                          │
                                          │ Rate-Limited HTTP/2 (Token Bucket)
                                          │ Exponential Jittered Backoff
                                          ▼
                   ┌───────────────────────────────────────────────┐
                   │               bcrs-tracker                    │
                   │                                               │
                   │  ┌─────────────────────────────────────────┐  │
                   │  │      Location Poller & Diff Engine      │  │
                   │  └────────────────────┬────────────────────┘  │
                   │                       │ Active RVM IDs        │
                   │                       ▼                       │
                   │  ┌─────────────────────────────────────────┐  │
                   │  │   Bin Worker Pool (Bounded Concurrency) │  │
                   │  └────────────────────┬────────────────────┘  │
                   │                       │                       │
                   │                       ▼                       │
                   │  ┌─────────────────────────────────────────┐  │
                   │  │    Batch Writer (500 rows / 5s flush)   │  │
                   │  └────────────────────┬────────────────────┘  │
                   └───────────────────────┼───────────────────────┘
                                           │
                                           │ Native Batch Insert (LZ4)
                                           ▼
                   ┌───────────────────────────────────────────────┐
                   │             ClickHouse Database               │
                   │                                               │
                   │   • rvm_current        (ReplacingMergeTree)   │
                   │   • rvm_events         (MergeTree)            │
                   │   • rvm_bin_snapshots  (MergeTree + 2yr TTL)  │
                   └───────────────────────────────────────────────┘
                                           ▲
                                           │ PromQL / SQL
┌──────────────────────────────┐           │
│       VictoriaMetrics        │───────────┘
│  (VMServiceScrape / VMRule)  │
└──────────────┬───────────────┘
               │
               ▼
┌──────────────────────────────┐
│           Grafana            │
│   (Pre-provisioned panels)   │
└──────────────────────────────┘
```

---

## Key Features

* **Resilient Upstream Poller**: Periodic polling of `/locations` (15m default) and `/bin-status` (5m default) with token-bucket rate limiting (`golang.org/x/time/rate`, 8 RPS, burst 16) and exponential jittered backoff on HTTP 429/5xx errors.
* **State Transition Diffing**: In-memory active machine cache diffs each poll cycle to detect `MACHINE_ADDED`, `MACHINE_REMOVED`, `STATUS_CHANGED`, and `METADATA_UPDATED` events.
* **Zero-Loss Schema Evolution**: Strongly typed analytical columns alongside compressed `raw_json String CODEC(ZSTD(3))` payloads ensure forward compatibility when upstream adds new attributes.
* **High-Throughput Batch Ingestion**: Background channel batch buffer accumulates rows and flushes to ClickHouse on size (500 rows) or timeout (5s), with graceful shutdown draining.
* **First-Class Observability**: Prometheus metrics endpoint (`:9090/metrics`), health endpoint (`:9090/healthz`), VictoriaMetrics `VMServiceScrape` and `VMRule` alerting, and Grafana dashboard provisioning.
* **Enterprise Security**: Runs as non-root (`UID 65532`), read-only root filesystem, dropped capabilities (`ALL`), and scratch/distroless base images.

---

## Storage Architecture & ClickHouse Schema

### Zero-Loss Schema Evolution

The database uses a **hybrid structured + raw storage strategy**:
1. **Typed Columns**: Commonly queried analytical dimensions (e.g. `id`, `serial_number`, `status`, `capacity`, `current_count`, `latitude`, `longitude`) are stored in strongly typed columns with `LowCardinality(String)` dictionaries and `ZSTD` codecs for optimal compression and fast OLAP queries.
2. **Raw JSON Payload**: The un-truncated upstream JSON is stored in `raw_json String CODEC(ZSTD(3))`. If upstream introduces new keys (e.g., `power_status`, `battery_level`), they are preserved immediately without requiring schema migrations.

### Table Schemas

```sql
CREATE DATABASE IF NOT EXISTS bcrs;

-- Table 1: Current state snapshot of all known RVMs
CREATE TABLE IF NOT EXISTS bcrs.rvm_current (
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
ORDER BY (id);

-- Table 2: Immutable event log for lifecycle and status transitions
CREATE TABLE IF NOT EXISTS bcrs.rvm_events (
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
ORDER BY (recorded_at, rvm_id, event_type);

-- Table 3: Time-series bin snapshot log for bottle counts and fill levels
CREATE TABLE IF NOT EXISTS bcrs.rvm_bin_snapshots (
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
TTL polled_at + INTERVAL 2 YEAR;
```

### Ad-Hoc JSON Queries

Extract unmapped or newly added fields directly from `raw_json`:

```sql
-- Query nested or newly introduced properties dynamically
SELECT
    id,
    location_name,
    JSONExtractString(raw_json, 'supplierName') AS supplier_name,
    JSONExtractFloat(raw_json, 'batteryLevel') AS battery_level
FROM bcrs.rvm_current
WHERE is_deleted = 0
LIMIT 10;
```

---

## Configuration Reference

All settings are configured via standard 12-factor environment variables:

| Variable | Default Value | Description | Example |
| :--- | :--- | :--- | :--- |
| `BCRS_BASE_URL` | `https://bts.bcrs.sg` | Base URL of the upstream BCRS API | `https://bts.bcrs.sg` |
| `LOCATION_POLL_INTERVAL` | `15m` | Interval between location directory poll cycles | `10m`, `30m` |
| `BIN_POLL_INTERVAL` | `5m` | Interval between bin status poll cycles | `3m`, `5m` |
| `BIN_WORKER_CONCURRENCY` | `8` | Number of concurrent bin scrape worker goroutines | `4`, `16` |
| `RATE_LIMIT_RPS` | `8.0` | Token bucket sustained request rate (requests/second) | `5.0`, `10.0` |
| `RATE_LIMIT_BURST` | `16` | Token bucket maximum burst capacity | `16`, `32` |
| `HTTP_TIMEOUT` | `10s` | Upstream HTTP client request timeout | `15s` |
| `CLICKHOUSE_ADDR` | `localhost:9000` | ClickHouse native TCP address | `clickhouse:9000` |
| `CLICKHOUSE_DATABASE` | `bcrs` | ClickHouse target database name | `bcrs` |
| `CLICKHOUSE_USER` | `default` | ClickHouse authentication username | `default` |
| `CLICKHOUSE_PASSWORD` | `""` (empty) | ClickHouse authentication password | `secretpass` |
| `CLICKHOUSE_BATCH_SIZE` | `500` | Number of buffered rows before triggering a batch insert | `1000` |
| `CLICKHOUSE_FLUSH_INTERVAL` | `5s` | Maximum duration to buffer rows before timed flush | `2s`, `10s` |
| `METRICS_PORT` | `9090` | HTTP server port for `/metrics` and `/healthz` | `9090` |
| `LOG_LEVEL` | `info` | Logging verbosity (`debug`, `info`, `warn`, `error`) | `debug` |

---

## Observability & Metrics

### Prometheus Metrics List

Exported on `http://<host>:9090/metrics`:

| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `bcrs_http_requests_total` | Counter | `endpoint`, `status_code` | Total HTTP requests sent to upstream BCRS APIs |
| `bcrs_http_request_duration_seconds` | Histogram | `endpoint` | Upstream HTTP request latency distribution |
| `bcrs_rate_limit_backoffs_total` | Counter | `endpoint` | Total HTTP 429 rate limit backoff events triggered |
| `bcrs_rvm_total` | Gauge | `status`, `supplier`, `coords_color` | Current count of active RVMs grouped by status and supplier |
| `bcrs_poll_cycle_duration_seconds` | Histogram | `type` (`locations`, `bins`) | Distribution of completed scrape cycle durations |
| `bcrs_poll_cycle_last_duration_seconds` | Gauge | `type` (`locations`, `bins`) | Duration in seconds of the most recent scrape cycle |
| `bcrs_poll_cycle_total` | Counter | `type`, `result` (`success`, `error`) | Total poll cycles completed |
| `bcrs_poll_cycle_last_success_timestamp_seconds` | Gauge | `type` | Unix timestamp of last successful poll cycle |
| `bcrs_clickhouse_rows_inserted_total` | Counter | `table` | Total rows committed to ClickHouse |
| `bcrs_clickhouse_insert_duration_seconds` | Histogram | `table` | ClickHouse batch insert latency |
| `bcrs_clickhouse_errors_total` | Counter | `table` | Total ClickHouse query and insertion errors |
| `bcrs_clickhouse_buffer_depth` | Gauge | `table` | Current pending rows in the batch channel |
| `bcrs_containers_ingested_total` | Counter | `compartment_type`, `supplier` | Cumulative total of containers (bottles/cans) returned |
| `bcrs_bin_current_count` | Gauge | `compartment_type`, `supplier` | Instantaneous count of containers sitting in RVM bins |

### Sample PromQL Queries

* **Total Lifetime Containers Returned**:
  ```promql
  sum(bcrs_containers_ingested_total)
  ```
* **Bottles & Cans Returned Per Hour (Smoothed Rolling Rate)**:
  ```promql
  sum by (compartment_type) (rate(bcrs_containers_ingested_total[30m])) * 3600
  ```
* **Hourly Container Return Volume**:
  ```promql
  sum by (compartment_type) (increase(bcrs_containers_ingested_total[1h]))
  ```
* **Total Containers Returned by Supplier**:
  ```promql
  sum by (supplier) (bcrs_containers_ingested_total)
  ```
* **Instantaneous Containers in Machine Bins**:
  ```promql
  sum by (compartment_type) (bcrs_bin_current_count)
  ```
* **Machine Status Distribution**:
  ```promql
  sum by (status) (bcrs_rvm_total)
  ```
* **National Machine Online Availability Percentage**:
  ```promql
  sum(bcrs_rvm_total{status="RUNNING"}) / sum(bcrs_rvm_total) * 100
  ```
* **Upstream Request Rate by HTTP Status**:
  ```promql
  sum by (status_code) (rate(bcrs_http_requests_total[5m]))
  ```
* **429 Rate Limit Backoff Rate**:
  ```promql
  rate(bcrs_rate_limit_backoffs_total[5m])
  ```
* **ClickHouse Ingestion Rate (rows/sec)**:
  ```promql
  sum by (table) (rate(bcrs_clickhouse_rows_inserted_total[5m]))
  ```
* **Time Since Last Successful Bin Scrape**:
  ```promql
  time() - max(bcrs_poll_cycle_last_success_timestamp_seconds{type="bins"})
  ```

### Alerting Rules (VMRule)

Defined in [`deployments/helm/bcrstracking/templates/vmrule.yaml`](deployments/helm/bcrstracking/templates/vmrule.yaml):

1. **`BCRSPollCycleFailing`** (`severity: warning`): Poll cycle error count > 3 in 10 minutes.
2. **`BCRSRateLimitExhausted`** (`severity: warning`): Scraper triggered > 5 rate limit backoff events in 10 minutes.
3. **`BCRSClickHouseInsertErrors`** (`severity: critical`): ClickHouse batch writer encountered insertion errors in the last 5 minutes.
4. **`BCRSBufferDepthHigh`** (`severity: warning`): Pending rows in ClickHouse batch buffer exceeds 1,000 items for > 2 minutes.
5. **`BCRSUpstreamAPIFailing`** (`severity: warning`): Upstream BCRS API 5xx failure rate exceeds 10% over 5 minutes.
6. **`BCRSScrapeStalled`** (`severity: critical`): Bin scrape cycle has not completed successfully for over 20 minutes.

### Grafana Dashboard

A pre-configured dashboard JSON is available in [`deployments/grafana/dashboards/bcrstracking.json`](deployments/grafana/dashboards/bcrstracking.json), featuring:
* **Executive Summary**: Total RVM count, online availability %, total items collected, scraper health.
* **National Status Breakdown**: Status pie charts, operational machines by supplier, postal district mapping.
* **Deposit Velocity & Capacity**: Hourly collection rate, top 10 busiest machines, bin full percentage trends.
* **Pipeline Telemetry**: HTTP request latency percentiles (p50/p90/p99), 429 backoff counters, ClickHouse buffer depth & insert duration.

---

## Local Development Guide

### Quickstart (Docker Compose)

Spin up the complete local observability & tracking stack with one command:

```bash
docker compose up --build -d
```

### Local Services & Ports

| Service | URL | Notes / Credentials |
| :--- | :--- | :--- |
| **bcrs-tracker** | `http://localhost:9090/metrics`<br>`http://localhost:9090/healthz` | Metrics endpoint & Health probe |
| **Grafana** | `http://localhost:3000` | Auto-provisioned (`admin` / `admin`) |
| **VictoriaMetrics** | `http://localhost:8428` | Prometheus-compatible TSDB & UI |
| **ClickHouse HTTP** | `http://localhost:8123` | Web client & HTTP query interface |
| **ClickHouse Native** | `localhost:9000` | TCP native client interface |

Stop the stack:
```bash
docker compose down
```

### Make Targets

| Command | Description |
| :--- | :--- |
| `make build` | Compile binary to `bin/bcrs-tracker` with optimizations (`-s -w`) |
| `make test` | Run full test suite with race detector and coverage profile |
| `make run` | Run service locally against a running ClickHouse instance |
| `make compose-up` | Build and start Docker Compose stack in background |
| `make compose-down` | Stop and remove Docker Compose containers |

---

## Production Deployment Guide (Kubernetes / Helm)

The Helm chart is located at [`deployments/helm/bcrstracking`](deployments/helm/bcrstracking) and published as an OCI artifact to GitLab Container Registry.

### Helm Installation

From local source:
```bash
helm install bcrs-tracker deployments/helm/bcrstracking \
  --namespace bcrs \
  --create-namespace \
  --set secret.clickhousePassword="your-clickhouse-password"
```

From OCI Registry:
```bash
helm install bcrs-tracker oci://<registry> \
  --version 0.1.0 \
  --namespace bcrs \
  --create-namespace \
  --set secret.clickhousePassword="your-clickhouse-password"
```

To dry-run or inspect generated manifests:

```bash
helm template bcrs-tracker deployments/helm/bcrstracking \
  --set secret.clickhousePassword="test"
```

### Chart Features

* **Dynamic Image Versioning**: Container image tag defaults to the Helm `.Chart.Version` without hardcoded values, allowing seamless version synchronization.
* **Official ClickHouse Operator Integration**: Automatically provisions `KeeperCluster` (3-replica consensus) and `ClickHouseCluster` (multi-replica analytical database) using official ClickHouse Operator CRDs (`clickhouse.com/v1alpha1`).
* **Security & Resource Best Practices**: `readOnlyRootFilesystem: true`, `runAsNonRoot: true`, `runAsUser: 65532`, dropped `ALL` capabilities, and CPU limits omitted to prevent throttling under load.
* **Probes**: Liveness and readiness HTTP probes configured on `/healthz` at port `9090`.
* **VictoriaMetrics Operator CRDs**:
  * `VMServiceScrape`: Automatically configures VictoriaMetrics operator scraping on port `9090` at 15s intervals.
  * `VMRule`: Automatically provisions production alert rules with configurable labels.
* **Grafana Dashboard Provisioning**: Deploys ConfigMap labeled `grafana_dashboard: "1"` for automatic sidecar ingestion into Grafana.

---

## CI/CD Pipeline & Dependency Automation
 
The project uses GitLab CI (`.gitlab-ci.yml`) with 4 automated stages and [Renovate](https://docs.renovatebot.com) (`renovate.json`) for automated dependency maintenance:
 
1. **`lint`**: Code quality and static analysis checks via `go vet ./...` on `golang:1.27-alpine`.
2. **`test`**: Race condition detection and coverage profiling with `go test -race -v -coverprofile=coverage.out ./...` on `golang:1.27-alpine`.
3. **`build`**: Container build and push using rootless [Kaniko](https://github.com/GoogleContainerTools/kaniko) executor (`gcr.io/kaniko-project/executor:v1.24.0-debug`) targeting `gcr.io/distroless/static-debian13:nonroot`. Automatically triggered on main branch commits (tagged `$CI_COMMIT_SHORT_SHA` and `latest`) and Git tags (tagged `$CI_COMMIT_TAG` and `latest`).
4. **`helm`**: Helm lint validation (`helm-lint`) and automated OCI chart packaging and publishing (`helm-publish`) to `${CI_REGISTRY_IMAGE}`.
