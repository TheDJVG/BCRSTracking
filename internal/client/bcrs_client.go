package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/TheDJVG/BCRSTracking/internal/config"
	"github.com/TheDJVG/BCRSTracking/internal/metrics"
	"github.com/TheDJVG/BCRSTracking/internal/model"
	"golang.org/x/time/rate"
)

// ErrTokenInvalidated is returned when an in-flight token acquisition is invalidated before completion.
var ErrTokenInvalidated = errors.New("token invalidated during fetch")

type tokenCall struct {
	done chan struct{}
	tok  string
	exp  time.Time
	err  error
}

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

// WithLogger overrides the logger used by Client.
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) {
		c.logger = l
	}
}

// WithToken initializes the client with a pre-configured token and expiry (useful in tests).
func WithToken(token string, expiresAt time.Time) Option {
	return func(c *Client) {
		c.token = token
		c.tokenExpiry = expiresAt
	}
}

// Client interacts with the upstream BCRS API.
type Client struct {
	baseURL        string
	httpClient     *http.Client
	rateLimiter    *rate.Limiter
	metrics        *metrics.Metrics
	logger         *slog.Logger
	initialBackoff time.Duration

	tokenMu      sync.RWMutex
	token        string
	tokenExpiry  time.Time
	tokenEpoch   uint64
	inFlightCall *tokenCall
}

// New creates a new BCRS API client.
func New(cfg *config.Config, m *metrics.Metrics, opts ...Option) *Client {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	}

	var baseURL string
	var timeout time.Duration = 10 * time.Second
	var rps float64 = 8.0
	var burst int = 16

	if cfg != nil {
		baseURL = strings.TrimRight(cfg.BaseURL, "/")
		if cfg.HTTPTimeout > 0 {
			timeout = cfg.HTTPTimeout
		}
		if cfg.RateLimitRPS > 0 {
			rps = cfg.RateLimitRPS
		}
		if cfg.RateLimitBurst > 0 {
			burst = cfg.RateLimitBurst
		}
	}

	c := &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		rateLimiter:    rate.NewLimiter(rate.Limit(rps), burst),
		metrics:        m,
		logger:         slog.Default(),
		initialBackoff: 500 * time.Millisecond,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// CachedToken returns the currently cached token and its expiration time.
func (c *Client) CachedToken() (string, time.Time) {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token, c.tokenExpiry
}

// InvalidateToken clears any currently cached access token.
func (c *Client) InvalidateToken() {
	c.invalidateToken("")
}

func (c *Client) invalidateToken(badToken string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if badToken == "" || c.token == badToken {
		c.token = ""
		c.tokenExpiry = time.Time{}
		if badToken == "" {
			c.tokenEpoch++
		}
	}
}

func (c *Client) getAccessToken(ctx context.Context) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		c.tokenMu.RLock()
		if c.token != "" && time.Now().Before(c.tokenExpiry) {
			tok := c.token
			c.tokenMu.RUnlock()
			return tok, nil
		}
		c.tokenMu.RUnlock()

		c.tokenMu.Lock()
		if err := ctx.Err(); err != nil {
			c.tokenMu.Unlock()
			return "", err
		}

		// Double-check under write lock
		if c.token != "" && time.Now().Before(c.tokenExpiry) {
			tok := c.token
			c.tokenMu.Unlock()
			return tok, nil
		}

		// If a token fetch is already in flight, wait for it
		if call := c.inFlightCall; call != nil {
			c.tokenMu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-call.done:
				if call.err == nil {
					return call.tok, nil
				}
				if err := ctx.Err(); err != nil {
					return "", err
				}
				// If the leader failed because its context was cancelled, timed out,
				// or the token was invalidated while in flight, retry with our own context.
				if errors.Is(call.err, context.Canceled) || errors.Is(call.err, context.DeadlineExceeded) || errors.Is(call.err, ErrTokenInvalidated) {
					continue
				}
				return "", call.err
			}
		}

		// We are the leader to fetch
		call := &tokenCall{done: make(chan struct{})}
		c.inFlightCall = call
		epoch := c.tokenEpoch
		c.tokenMu.Unlock()

		var tok string
		var expiry time.Time
		var err error

		func() {
			defer func() {
				if r := recover(); r != nil {
					c.tokenMu.Lock()
					c.inFlightCall = nil
					call.err = fmt.Errorf("token fetch panic: %v", r)
					close(call.done)
					c.tokenMu.Unlock()
					panic(r)
				}
			}()
			tok, expiry, err = c.fetchAccessToken(ctx)
		}()

		c.tokenMu.Lock()
		c.inFlightCall = nil
		if err == nil {
			if c.tokenEpoch == epoch {
				c.token = tok
				c.tokenExpiry = expiry
				call.tok = tok
				call.exp = expiry
			} else {
				err = ErrTokenInvalidated
			}
		}
		call.err = err
		close(call.done)
		epochAfter := c.tokenEpoch
		c.tokenMu.Unlock()

		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return "", err
			}
			if (epochAfter != epoch || errors.Is(err, ErrTokenInvalidated)) && ctx.Err() == nil {
				continue
			}
			return "", err
		}
		return tok, nil
	}
}

func isHTMLPayload(body []byte) bool {
	sample := body
	if len(sample) > 512 {
		sample = sample[:512]
	}
	trimmed := strings.TrimSpace(string(sample))
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "<!doctype") ||
		strings.HasPrefix(lower, "<html") ||
		strings.HasPrefix(lower, "<head") ||
		strings.Contains(lower, "cloudflare") ||
		strings.Contains(lower, "challenge-platform")
}

// EndpointURL resolves a given API subpath against the client's baseURL.
// If baseURL does not already contain an API path prefix (such as /forapi/v2),
// it automatically prefixes the subpath with /forapi/v2.
func (c *Client) EndpointURL(subpath string) string {
	base := strings.TrimRight(c.baseURL, "/")
	subpath = "/" + strings.TrimLeft(subpath, "/")
	if strings.HasSuffix(base, "/forapi/v2") {
		return base + subpath
	}
	u, err := url.Parse(base)
	if err == nil && (u.Path == "" || u.Path == "/") {
		return base + "/forapi/v2" + subpath
	}
	return base + subpath
}

func (c *Client) fetchAccessToken(ctx context.Context) (string, time.Time, error) {
	url := c.EndpointURL("/locations/access-token")
	body, err := c.doWithRetry(ctx, "/locations/access-token", url, false, 3)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to fetch access token: %w", err)
	}

	var resp model.AccessTokenResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		if isHTMLPayload(body) {
			return "", time.Time{}, fmt.Errorf("upstream returned HTML instead of JSON (possible Cloudflare challenge or WAF block): %w", err)
		}
		return "", time.Time{}, fmt.Errorf("failed to parse access token response: %w", err)
	}

	expiry := resp.ExpiresAt
	if expiry.IsZero() || !expiry.After(time.Now()) {
		expiry = time.Now().Add(1 * time.Hour)
	}

	return resp.Token, expiry, nil
}

// GetLocations fetches all RVM locations from the BCRS API.
func (c *Client) GetLocations(ctx context.Context) ([]model.RVM, error) {
	url := c.EndpointURL("/locations")
	body, err := c.doWithRetry(ctx, "/locations", url, true, 3)
	if err != nil {
		return nil, err
	}

	var resp model.LocationsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		if isHTMLPayload(body) {
			return nil, fmt.Errorf("upstream returned HTML instead of JSON (possible Cloudflare challenge or WAF block): %w", err)
		}
		return nil, fmt.Errorf("failed to unmarshal locations response: %w", err)
	}
	return resp.Data, nil
}

// GetBinStatus fetches the current bin status for a specific RVM.
func (c *Client) GetBinStatus(ctx context.Context, rvmID uint32) ([]model.BinStatus, error) {
	url := c.EndpointURL(fmt.Sprintf("/locations/rvms/%d/bin-status", rvmID))
	body, err := c.doWithRetry(ctx, "/bin-status", url, true, 3)
	if err != nil {
		return nil, err
	}

	var resp model.BinStatusResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		if isHTMLPayload(body) {
			return nil, fmt.Errorf("upstream returned HTML instead of JSON (possible Cloudflare challenge or WAF block): %w", err)
		}
		return nil, fmt.Errorf("failed to unmarshal bin status response: %w", err)
	}
	return resp.Data, nil
}

func computeJitter(backoff time.Duration) time.Duration {
	if backoff <= 0 {
		return 0
	}
	maxJitter := backoff
	if maxJitter > time.Second {
		maxJitter = time.Second
	}
	return time.Duration(rand.Int63n(int64(maxJitter) + 1))
}

func (c *Client) doWithRetry(ctx context.Context, endpointKey, url string, requireAuth bool, maxRetries int) ([]byte, error) {
	var lastErr error
	backoff := c.initialBackoff
	if backoff <= 0 {
		backoff = 10 * time.Millisecond
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		var usedToken string
		if requireAuth {
			tok, err := c.getAccessToken(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to obtain access token: %w", err)
			}
			if tok == "" {
				return nil, fmt.Errorf("obtained empty access token")
			}
			usedToken = tok
		}

		if err := c.rateLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limiter wait failed: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("User-Agent", "BCRSTracking/1.0 (+https://github.com/TheDJVG/BCRSTracking)")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("x-bcrs-client", "web")
		req.Header.Set("Referer", "https://bts.bcrs.sg/")

		if requireAuth && usedToken != "" {
			req.Header.Set("x-bcrs-map-token", usedToken)
		}

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

			jitter := computeJitter(backoff)
			if err := sleepWithContext(ctx, backoff+jitter); err != nil {
				return nil, err
			}
			backoff *= 2
			continue
		}

		if c.metrics != nil {
			c.metrics.RecordHTTPRequest(endpointKey, resp.StatusCode, duration)
		}

		if resp.StatusCode == http.StatusForbidden {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if requireAuth {
				c.invalidateToken(usedToken)
			}
			if c.metrics != nil {
				c.metrics.RecordAuthError(endpointKey)
			}
			if c.logger != nil {
				msg := "upstream 403 forbidden received"
				if requireAuth {
					msg = "upstream 403 forbidden received, invalidated access token"
				}
				c.logger.ErrorContext(ctx, msg,
					"endpoint", endpointKey,
					"attempt", attempt,
					"max_retries", maxRetries,
					"url", url,
				)
			}
			lastErr = fmt.Errorf("upstream forbidden 403 received")

			if attempt == maxRetries {
				break
			}

			jitter := computeJitter(backoff)
			if err := sleepWithContext(ctx, backoff+jitter); err != nil {
				return nil, err
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if c.metrics != nil {
				c.metrics.RecordRateLimitBackoff(endpointKey)
			}
			lastErr = fmt.Errorf("upstream rate limit 429 received")

			if attempt == maxRetries {
				break
			}

			jitter := computeJitter(backoff)
			if err := sleepWithContext(ctx, backoff+jitter); err != nil {
				return nil, err
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode >= 500 {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			lastErr = fmt.Errorf("upstream server error: %d", resp.StatusCode)

			if attempt == maxRetries {
				break
			}

			jitter := computeJitter(backoff)
			if err := sleepWithContext(ctx, backoff+jitter); err != nil {
				return nil, err
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return nil, fmt.Errorf("upstream returned unexpected status code: %d", resp.StatusCode)
		}

		const maxBodySize = 10 << 20 // 10MB
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
		if len(body) > maxBodySize {
			return nil, fmt.Errorf("response body exceeded %d byte limit", maxBodySize)
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
