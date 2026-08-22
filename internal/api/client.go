// Package api is a client for the MarvelCDB HTTP API: the public card, pack,
// faction, FAQ and decklist endpoints, and the OAuth2-protected deck endpoints.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// DefaultBaseURL is the canonical MarvelCDB origin.
const DefaultBaseURL = "https://marvelcdb.com"

// DefaultUserAgent identifies the CLI to MarvelCDB. A community site deserves a
// request it can attribute.
const DefaultUserAgent = "marvelcdb-cli (+https://github.com/mggarofalo/marvelcdb-cli)"

// oauthPrefix is the path prefix behind MarvelCDB's OAuth2 firewall.
const oauthPrefix = "/api/oauth2/"

// Locales MarvelCDB serves from a language subdomain. "en" means the bare
// marvelcdb.com host.
var Locales = []string{"en", "de", "es", "fr", "it", "ko"}

// maxResponseBytes bounds a single response. The full card corpus is ~7 MB, so
// this leaves generous headroom while refusing to buffer without limit.
const maxResponseBytes = 128 << 20

// Client talks to one MarvelCDB origin.
type Client struct {
	baseURL   *url.URL
	http      *http.Client
	userAgent string
}

// Option configures a Client.
type Option func(*builder)

type builder struct {
	baseURL     string
	locale      string
	userAgent   string
	transport   http.RoundTripper
	httpClient  *http.Client
	retry       RetryPolicy
	minInterval time.Duration
	timeout     time.Duration
	tokens      oauth2.TokenSource
}

// WithBaseURL overrides the MarvelCDB origin, for tests or a mirror.
func WithBaseURL(raw string) Option {
	return func(b *builder) {
		if raw != "" {
			b.baseURL = raw
		}
	}
}

// WithLocale selects a translation. It rewrites the host to the matching
// language subdomain and is ignored when the base URL is not marvelcdb.com.
func WithLocale(locale string) Option {
	return func(b *builder) { b.locale = strings.ToLower(strings.TrimSpace(locale)) }
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(b *builder) {
		if ua != "" {
			b.userAgent = ua
		}
	}
}

// WithTransport installs the transport the client's own middleware wraps. Use
// it to insert the on-disk HTTP cache.
func WithTransport(rt http.RoundTripper) Option {
	return func(b *builder) { b.transport = rt }
}

// WithHTTPClient replaces the whole client, bypassing retry, rate limiting and
// User-Agent stamping. Mostly useful in tests.
func WithHTTPClient(hc *http.Client) Option {
	return func(b *builder) { b.httpClient = hc }
}

// WithRetryPolicy overrides the transient-failure retry behaviour.
func WithRetryPolicy(p RetryPolicy) Option {
	return func(b *builder) { b.retry = p }
}

// WithRateLimit sets the minimum interval between outbound requests.
func WithRateLimit(d time.Duration) Option {
	return func(b *builder) { b.minInterval = d }
}

// WithTimeout bounds a single request, including retries.
func WithTimeout(d time.Duration) Option {
	return func(b *builder) { b.timeout = d }
}

// WithTokenSource supplies OAuth2 credentials. The bearer token is attached
// only to /api/oauth2/ requests, so public reads stay anonymous and cacheable.
func WithTokenSource(ts oauth2.TokenSource) Option {
	return func(b *builder) { b.tokens = ts }
}

// New builds a Client.
func New(opts ...Option) (*Client, error) {
	b := &builder{
		baseURL:     DefaultBaseURL,
		userAgent:   DefaultUserAgent,
		retry:       DefaultRetryPolicy(),
		minInterval: 100 * time.Millisecond,
		timeout:     60 * time.Second,
	}
	for _, opt := range opts {
		opt(b)
	}

	base, err := url.Parse(b.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url %q: %w", b.baseURL, err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("base url %q must be absolute", b.baseURL)
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	applyLocale(base, b.locale)

	c := &Client{baseURL: base, userAgent: b.userAgent}

	if b.httpClient != nil {
		c.http = b.httpClient
		return c, nil
	}

	rt := b.transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	if b.tokens != nil {
		rt = &authTransport{base: rt, tokens: b.tokens}
	}
	rt = &retryTransport{base: rt, policy: b.retry}
	if b.minInterval > 0 {
		rt = &rateLimitTransport{base: rt, interval: b.minInterval}
	}
	rt = &headerTransport{base: rt, userAgent: b.userAgent}

	c.http = &http.Client{Transport: rt, Timeout: b.timeout}
	return c, nil
}

// applyLocale points the host at a MarvelCDB language subdomain.
func applyLocale(base *url.URL, locale string) {
	if locale == "" || locale == "en" {
		return
	}
	host := base.Hostname()
	if !strings.HasSuffix(host, "marvelcdb.com") {
		return
	}
	host = strings.TrimPrefix(host, "www.")
	// Replace any existing language subdomain rather than stacking one on.
	if i := strings.Index(host, "."); i > 0 && strings.Count(host, ".") > 1 {
		host = host[i+1:]
	}
	newHost := locale + "." + host
	if port := base.Port(); port != "" {
		newHost = net.JoinHostPort(newHost, port)
	}
	base.Host = newHost
}

// BaseURL reports the origin this client talks to.
func (c *Client) BaseURL() string { return c.baseURL.String() }

// authTransport attaches the OAuth2 bearer token to protected requests only.
type authTransport struct {
	base   http.RoundTripper
	tokens oauth2.TokenSource
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.Contains(req.URL.Path, oauthPrefix) {
		return t.base.RoundTrip(req)
	}
	tok, err := t.tokens.Token()
	if err != nil {
		return nil, fmt.Errorf("obtain access token: %w", err)
	}
	req = req.Clone(req.Context())
	tok.SetAuthHeader(req)
	return t.base.RoundTrip(req)
}

// request describes one call to MarvelCDB.
type request struct {
	method string
	path   string
	query  url.Values
	form   url.Values
	// singleRecord marks endpoints that address one record by id. MarvelCDB
	// answers those with a 500 error envelope when the id is unknown, which the
	// error layer translates into ErrNotFound.
	singleRecord bool
}

// do executes r and decodes a JSON response into out. out may be nil.
func (c *Client) do(ctx context.Context, r request, out any) error {
	u := *c.baseURL
	u.Path = c.baseURL.Path + r.path
	if len(r.query) > 0 {
		u.RawQuery = r.query.Encode()
	}
	endpoint := u.String()

	var body io.Reader
	if r.form != nil {
		body = strings.NewReader(r.form.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, r.method, endpoint, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if r.form != nil {
		encoded := r.form.Encode()
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.ContentLength = int64(len(encoded))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(encoded)), nil
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("%s %s: %w", r.method, endpoint, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read response from %s: %w", endpoint, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return newError(r.method, endpoint, resp.StatusCode, payload, r.singleRecord)
	}

	if out == nil {
		return nil
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode response from %s: %w", endpoint, err)
	}
	return nil
}
