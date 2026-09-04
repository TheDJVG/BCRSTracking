package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	Registry *prometheus.Registry

	HTTPRequestsTotal        *prometheus.CounterVec
	HTTPRequestDuration      *prometheus.HistogramVec
	RateLimitBackoffsTotal   *prometheus.CounterVec
	RVMTotal                 *prometheus.GaugeVec
	PollCycleDuration        *prometheus.HistogramVec
	PollCycleLastDuration    *prometheus.GaugeVec
	PollCycleTotal           *prometheus.CounterVec
	PollCycleLastSuccess     *prometheus.GaugeVec
	ClickHouseRowsInserted   *prometheus.CounterVec
	ClickHouseInsertDuration *prometheus.HistogramVec
	ClickHouseErrorsTotal    *prometheus.CounterVec
	ClickHouseBufferDepth    *prometheus.GaugeVec
	ContainersIngestedTotal  *prometheus.CounterVec
	BinCurrentCount          *prometheus.GaugeVec
}

func New() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		Registry: reg,
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "bcrs_http_requests_total",
				Help: "Total HTTP requests sent to upstream BCRS APIs",
			},
			[]string{"endpoint", "status_code"},
		),
		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "bcrs_http_request_duration_seconds",
				Help:    "Upstream HTTP request latency distribution",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"endpoint"},
		),
		RateLimitBackoffsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "bcrs_rate_limit_backoffs_total",
				Help: "Total 429 rate limit backoff events triggered",
			},
			[]string{"endpoint"},
		),
		RVMTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "bcrs_rvm_total",
				Help: "Current count of active RVMs grouped by status and supplier",
			},
			[]string{"status", "supplier", "coords_color"},
		),
		PollCycleDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "bcrs_poll_cycle_duration_seconds",
				Help:    "Duration of completed scrape cycles",
				Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600},
			},
			[]string{"type"},
		),
		PollCycleLastDuration: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "bcrs_poll_cycle_last_duration_seconds",
				Help: "Duration in seconds of the most recent scrape cycle",
			},
			[]string{"type"},
		),
		PollCycleTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "bcrs_poll_cycle_total",
				Help: "Total poll cycles completed",
			},
			[]string{"type", "result"},
		),
		PollCycleLastSuccess: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "bcrs_poll_cycle_last_success_timestamp_seconds",
				Help: "Unix timestamp of last successful poll cycle",
			},
			[]string{"type"},
		),
		ClickHouseRowsInserted: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "bcrs_clickhouse_rows_inserted_total",
				Help: "Total rows committed to ClickHouse",
			},
			[]string{"table"},
		),
		ClickHouseInsertDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "bcrs_clickhouse_insert_duration_seconds",
				Help:    "ClickHouse batch insert latency",
				Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5},
			},
			[]string{"table"},
		),
		ClickHouseErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "bcrs_clickhouse_errors_total",
				Help: "Total ClickHouse query and insertion errors",
			},
			[]string{"table"},
		),
		ClickHouseBufferDepth: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "bcrs_clickhouse_buffer_depth",
				Help: "Current pending rows in the batch channel",
			},
			[]string{"table"},
		),
		ContainersIngestedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "bcrs_containers_ingested_total",
				Help: "Cumulative total of containers (bottles/cans) returned to RVMs",
			},
			[]string{"compartment_type", "supplier"},
		),
		BinCurrentCount: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "bcrs_bin_current_count",
				Help: "Current instantaneous count of containers sitting in RVM bins",
			},
			[]string{"compartment_type", "supplier"},
		),
	}

	reg.MustRegister(
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.RateLimitBackoffsTotal,
		m.RVMTotal,
		m.PollCycleDuration,
		m.PollCycleLastDuration,
		m.PollCycleTotal,
		m.PollCycleLastSuccess,
		m.ClickHouseRowsInserted,
		m.ClickHouseInsertDuration,
		m.ClickHouseErrorsTotal,
		m.ClickHouseBufferDepth,
		m.ContainersIngestedTotal,
		m.BinCurrentCount,
	)

	// Pre-initialize label values so metrics are immediately visible upon startup
	for _, pType := range []string{"locations", "bins"} {
		m.PollCycleDuration.WithLabelValues(pType)
		m.PollCycleLastDuration.WithLabelValues(pType).Set(0)
		m.PollCycleTotal.WithLabelValues(pType, "success")
		m.PollCycleTotal.WithLabelValues(pType, "error")
		m.PollCycleLastSuccess.WithLabelValues(pType).Set(0)
	}

	for _, supp := range []string{"TOMRA001", "SGRECYCLE001", "RVMS001"} {
		for _, cType := range []string{"PLASTIC", "ALUMINIUM", "CAN", "BOTTLE", "CAN PET", "PET", "Bottle Can", "UNKNOWN"} {
			m.ContainersIngestedTotal.WithLabelValues(cType, supp).Add(0)
			m.BinCurrentCount.WithLabelValues(cType, supp).Set(0)
		}
	}

	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}

func (m *Metrics) RecordHTTPRequest(endpoint string, statusCode int, duration time.Duration) {
	m.HTTPRequestsTotal.WithLabelValues(endpoint, strconv.Itoa(statusCode)).Inc()
	m.HTTPRequestDuration.WithLabelValues(endpoint).Observe(duration.Seconds())
}

func (m *Metrics) RecordRateLimitBackoff(endpoint string) {
	m.RateLimitBackoffsTotal.WithLabelValues(endpoint).Inc()
}

func (m *Metrics) SetRVMTotal(status, supplier, coordsColor string, count float64) {
	m.RVMTotal.WithLabelValues(status, supplier, coordsColor).Set(count)
}

func (m *Metrics) RecordPollCycle(pollType string, duration time.Duration, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	m.PollCycleTotal.WithLabelValues(pollType, result).Inc()
	m.PollCycleDuration.WithLabelValues(pollType).Observe(duration.Seconds())
	if m.PollCycleLastDuration != nil {
		m.PollCycleLastDuration.WithLabelValues(pollType).Set(duration.Seconds())
	}
	if err == nil {
		m.PollCycleLastSuccess.WithLabelValues(pollType).Set(float64(time.Now().Unix()))
	}
}

func (m *Metrics) RecordContainersIngested(compartmentType, supplier string, delta uint32) {
	if m != nil && m.ContainersIngestedTotal != nil {
		m.ContainersIngestedTotal.WithLabelValues(compartmentType, supplier).Add(float64(delta))
	}
}

func (m *Metrics) SetBinCurrentCount(compartmentType, supplier string, count float64) {
	if m != nil && m.BinCurrentCount != nil {
		m.BinCurrentCount.WithLabelValues(compartmentType, supplier).Set(count)
	}
}

func (m *Metrics) ResetBinCurrentCounts() {
	if m != nil && m.BinCurrentCount != nil {
		m.BinCurrentCount.Reset()
	}
}

func (m *Metrics) RecordClickHouseInsert(table string, rows int, duration time.Duration, err error) {
	if m == nil {
		return
	}
	if err != nil {
		m.ClickHouseErrorsTotal.WithLabelValues(table).Inc()
		return
	}
	m.ClickHouseRowsInserted.WithLabelValues(table).Add(float64(rows))
	m.ClickHouseInsertDuration.WithLabelValues(table).Observe(duration.Seconds())
}

func (m *Metrics) SetClickHouseBufferDepth(table string, depth float64) {
	if m != nil && m.ClickHouseBufferDepth != nil {
		m.ClickHouseBufferDepth.WithLabelValues(table).Set(depth)
	}
}
