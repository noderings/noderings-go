// Package noderings is the Go SDK for the NodeRings API: virtual machines,
// DNS, firewalls, snapshots and backups, game servers, the marketplace,
// billing, and the provider (seller) side of NodeRings.
//
// Create a client with a service account token and call the service for the
// part of the API you need:
//
//	client, err := noderings.NewClient(os.Getenv("NODERINGS_TOKEN"))
//	if err != nil { ... }
//	vms, err := client.VirtualMachine.ListVirtualMachines(ctx, nil)
//
// Every API area is a field of Client (VirtualMachine, DNSZone, DNSRecord,
// Firewall, Marketplace, ProviderPlan, and so on). Each method calls one API
// operation; list operations that page also have an ...All method that
// returns an iterator over every result.
package noderings

//go:generate go run ./internal/gen -spec api/openapi.json -out .

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Version is the SDK version, sent in the User-Agent header.
const Version = "0.1.0"

// DefaultBaseURL is the public NodeRings API.
const DefaultBaseURL = "https://api.noderings.com"

const defaultPageSize = 100

// Client calls the NodeRings API. It is safe for concurrent use. Create one
// with NewClient and reuse it.
type Client struct {
	services

	baseURL      *url.URL
	token        string
	organization string
	userAgent    string
	http         *http.Client
	maxRetries   int
	minBackoff   time.Duration
	maxBackoff   time.Duration
	logger       *slog.Logger
}

// Option configures a Client.
type Option func(*Client) error

// WithOrganization sends every request in the given organization (the
// X-Organization-ID header). Without it the API uses the token's organization.
func WithOrganization(id string) Option {
	return func(c *Client) error { c.organization = strings.TrimSpace(id); return nil }
}

// WithBaseURL points the client at another API endpoint, for example a test
// environment. The default is DefaultBaseURL.
func WithBaseURL(raw string) Option {
	return func(c *Client) error {
		u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("noderings: invalid base URL %q", raw)
		}
		c.baseURL = u
		return nil
	}
}

// WithHTTPClient sets the HTTP client used for requests. The default has a
// 60-second timeout.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) error {
		if h == nil {
			return errors.New("noderings: nil HTTP client")
		}
		c.http = h
		return nil
	}
}

// WithUserAgent adds a product token, such as "my-app/1.2", in front of the
// SDK's own User-Agent.
func WithUserAgent(ua string) Option {
	return func(c *Client) error {
		if ua = strings.TrimSpace(ua); ua != "" {
			c.userAgent = ua + " " + c.userAgent
		}
		return nil
	}
}

// WithRetries sets how often a failed request is retried (default 3) and the
// first and largest wait between attempts (default 500 ms and 10 s). See
// Client for which requests are retried. Use 0 retries to turn retrying off.
func WithRetries(maxRetries int, minBackoff, maxBackoff time.Duration) Option {
	return func(c *Client) error {
		if maxRetries < 0 || minBackoff < 0 || maxBackoff < minBackoff {
			return errors.New("noderings: invalid retry settings")
		}
		c.maxRetries, c.minBackoff, c.maxBackoff = maxRetries, minBackoff, maxBackoff
		return nil
	}
}

// WithLogger logs one debug line per HTTP request (method, path, status,
// duration). Tokens and bodies are never logged.
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) error { c.logger = l; return nil }
}

// NewClient returns a client that authenticates with token, a NodeRings
// service account token.
//
// Requests are retried when the API is rate limiting (HTTP 429, unless a plan
// quota is exhausted), and on connection errors and HTTP 502, 503 and 504
// when the request is safe to repeat: reads, PUT and DELETE, and creates that
// carry an idempotency key.
func NewClient(token string, opts ...Option) (*Client, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("noderings: a token is required")
	}
	base, _ := url.Parse(DefaultBaseURL)
	c := &Client{
		baseURL:    base,
		token:      token,
		userAgent:  "noderings-go/" + Version,
		http:       &http.Client{Timeout: 60 * time.Second},
		maxRetries: 3,
		minBackoff: 500 * time.Millisecond,
		maxBackoff: 10 * time.Second,
		logger:     slog.New(slog.DiscardHandler),
	}
	for _, o := range opts {
		if err := o(c); err != nil {
			return nil, err
		}
	}
	c.initServices()
	return c, nil
}

// request is one API call, built by the generated services.
type request struct {
	method string
	path   string
	query  url.Values
	body   any
	// idempotent marks a non-GET request that is safe to retry because it
	// carries an idempotency key.
	idempotent bool
}

func bodyOrNil[T any](b *T) any {
	if b == nil {
		return nil
	}
	return b
}

// Do sends a request to any API path, for operations the SDK does not cover
// yet. body is encoded as JSON when not nil and the response is decoded into
// out when out is not nil.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	return c.call(ctx, &request{method: method, path: path, query: query, body: body}, out)
}

func (c *Client) call(ctx context.Context, r *request, out any) error {
	var payload []byte
	if r.body != nil {
		b, err := json.Marshal(r.body)
		if err != nil {
			return fmt.Errorf("noderings: encoding request body: %w", err)
		}
		payload = b
	}
	// r.path is already escaped segment by segment; keep it exactly as is.
	target := strings.TrimRight(c.baseURL.String(), "/") + r.path
	if len(r.query) > 0 {
		target += "?" + r.query.Encode()
	}
	safe := r.method == http.MethodGet || r.method == http.MethodHead || r.method == http.MethodPut ||
		r.method == http.MethodDelete || r.idempotent

	for attempt := 0; ; attempt++ {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, r.method, target, body)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.organization != "" {
			req.Header.Set("X-Organization-ID", c.organization)
		}
		start := time.Now()
		resp, err := c.http.Do(req)
		if err != nil {
			c.logger.DebugContext(ctx, "noderings request failed", "method", r.method, "path", r.path, "err", err)
			if ctx.Err() != nil || !safe || attempt >= c.maxRetries {
				return err
			}
			if werr := c.wait(ctx, attempt, 0); werr != nil {
				return err
			}
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		_ = resp.Body.Close()
		c.logger.DebugContext(ctx, "noderings request", "method", r.method, "path", r.path,
			"status", resp.StatusCode, "duration", time.Since(start))
		if readErr != nil {
			return fmt.Errorf("noderings: reading response: %w", readErr)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out == nil || len(bytes.TrimSpace(data)) == 0 {
				return nil
			}
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("noderings: decoding %s %s response: %w", r.method, r.path, err)
			}
			return nil
		}
		apiErr := decodeError(resp, data, r)
		if attempt >= c.maxRetries || !retryable(apiErr, safe) {
			return apiErr
		}
		if werr := c.wait(ctx, attempt, retryAfter(resp)); werr != nil {
			return apiErr
		}
	}
}

func retryable(e *Error, safe bool) bool {
	switch e.StatusCode {
	case http.StatusTooManyRequests:
		return !e.IsQuotaExceeded()
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return safe
	}
	return false
}

func retryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}

func (c *Client) wait(ctx context.Context, attempt int, hint time.Duration) error {
	d := c.minBackoff << min(attempt, 16)
	if d > c.maxBackoff || d <= 0 {
		d = c.maxBackoff
	}
	d = d/2 + time.Duration(rand.Int64N(int64(d)/2+1)) // jitter
	if hint > d {
		d = min(hint, c.maxBackoff)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
