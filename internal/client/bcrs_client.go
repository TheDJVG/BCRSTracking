package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"

	"github.com/TheDJVG/BCRSTracking/internal/config"
	"github.com/TheDJVG/BCRSTracking/internal/metrics"
	"github.com/TheDJVG/BCRSTracking/internal/model"
	"golang.org/x/time/rate"
)

// Option allows customizing Client configurations.
type Option func(*Client)

// WithInitialBackoff overrides the initial backoff duration (useful in tests).
func WithInitialBackoff(d time.Duration) Option {
	return func(c *Client) {
		c.initialBackoff = d
	}
}

// WithHTTPClient overrides the underlying http.Client.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// Client interacts with the upstream BCRS API.
type Client struct {
	baseURL        string
	httpClient     *http.Client
	rateLimiter    *rate.Limiter
	metrics        *metrics.Metrics
	initialBackoff time.Duration
}

// New creates a new BCRS API client.
func New(cfg *config.Config, m *metrics.Metrics, opts ...Option) *Client {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	}

	c := &Client{
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   cfg.HTTPTimeout,
		},
		rateLimiter:    rate.NewLimiter(rate.Limit(cfg.RateLimitRPS), cfg.RateLimitBurst),
		metrics:        m,
		initialBackoff: 500 * time.Millisecond,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// GetLocations fetches all RVM locations from the BCRS API.
func (c *Client) GetLocations(ctx context.Context) ([]model.RVM, error) {
	url := fmt.Sprintf("%s/api/v1/locations", c.baseURL)
	body, err := c.doWithRetry(ctx, "/locations", url, 3)
	if err != nil {
		return nil, err
	}

	var resp model.LocationsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal locations response: %w", err)
	}
	return resp.Data, nil
}

// GetBinStatus fetches the current bin status for a specific RVM.
func (c *Client) GetBinStatus(ctx context.Context, rvmID uint32) ([]model.BinStatus, error) {
	url := fmt.Sprintf("%s/api/v1/locations/rvms/%d/bin-status", c.baseURL, rvmID)
	body, err := c.doWithRetry(ctx, "/bin-status", url, 3)
	if err != nil {
		return nil, err
	}

	var resp model.BinStatusResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal bin status response: %w", err)
	}
	return resp.Data, nil
}

func (c *Client) doWithRetry(ctx context.Context, endpointKey, url string, maxRetries int) ([]byte, error) {
	var lastErr error
	backoff := c.initialBackoff

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := c.rateLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limiter wait failed: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("User-Agent", "BCRSTracking/1.0 (+https://github.com/TheDJVG/BCRSTracking)")
		req.Header.Set("Accept", "application/json")

		start := time.Now()
		resp, err := c.httpClient.Do(req)
		duration := time.Since(start)

		if err != nil {
			lastErr = err
			if c.metrics != nil {
				c.metrics.RecordHTTPRequest(endpointKey, 0, duration)
			}

			if attempt == maxRetries {
				break
			}

			jitter := time.Duration(0)
			if backoff > 100*time.Millisecond {
				jitter = time.Duration(rand.Intn(200)) * time.Millisecond
			}
			if err := sleepWithContext(ctx, backoff+jitter); err != nil {
				return nil, err
			}
			backoff *= 2
			continue
		}

		if c.metrics != nil {
			c.metrics.RecordHTTPRequest(endpointKey, resp.StatusCode, duration)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			if c.metrics != nil {
				c.metrics.RecordRateLimitBackoff(endpointKey)
			}
			lastErr = fmt.Errorf("upstream rate limit 429 received")

			if attempt == maxRetries {
				break
			}

			jitter := time.Duration(0)
			if backoff > 100*time.Millisecond {
				jitter = time.Duration(rand.Intn(500)) * time.Millisecond
			}
			if err := sleepWithContext(ctx, backoff+jitter); err != nil {
				return nil, err
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("upstream server error: %d", resp.StatusCode)

			if attempt == maxRetries {
				break
			}

			if err := sleepWithContext(ctx, backoff); err != nil {
				return nil, err
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, fmt.Errorf("upstream returned unexpected status code: %d", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
		return body, nil
	}

	return nil, fmt.Errorf("request failed after %d retries: %w", maxRetries, lastErr)
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
