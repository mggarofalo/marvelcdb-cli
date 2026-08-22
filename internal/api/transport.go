package api

import (
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RetryPolicy controls how the client reacts to transient failures.
type RetryPolicy struct {
	// MaxAttempts counts the initial request, so 1 disables retrying.
	MaxAttempts int
	// BaseDelay is the first backoff interval; it doubles per attempt.
	BaseDelay time.Duration
	// MaxDelay caps the backoff, before Retry-After is taken into account.
	MaxDelay time.Duration
}

// DefaultRetryPolicy retries a handful of times over roughly ten seconds.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 4, BaseDelay: 400 * time.Millisecond, MaxDelay: 5 * time.Second}
}

// retryTransport retries idempotent-looking requests that fail transiently.
//
// HTTP 500 is deliberately not retried: MarvelCDB returns it for ids it does
// not have, so retrying multiplies load without ever succeeding.
type retryTransport struct {
	base   http.RoundTripper
	policy RetryPolicy
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	attempts := max(t.policy.MaxAttempts, 1)

	// A request whose body cannot be rewound can only be sent once.
	if req.Body != nil && req.GetBody == nil {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; ; attempt++ {
		outbound := req
		if attempt > 1 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("rewind request body: %w", err)
			}
			outbound = req.Clone(req.Context())
			outbound.Body = body
		}

		resp, err := t.base.RoundTrip(outbound)
		switch {
		case err != nil:
			if !retryableErr(err) {
				return nil, err
			}
			lastErr = err
		case !retryableStatus(resp.StatusCode):
			return resp, nil
		default:
			lastErr = fmt.Errorf("upstream returned %s", resp.Status)
		}

		if attempt >= attempts {
			if resp != nil {
				return resp, nil
			}
			return nil, lastErr
		}

		delay := t.backoff(attempt, resp)
		if resp != nil {
			// The body must be drained and closed or the connection leaks.
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			_ = resp.Body.Close()
		}

		timer := time.NewTimer(delay)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
}

func (t *retryTransport) backoff(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if d, ok := retryAfter(resp); ok {
			return min(d, 2*t.policy.MaxDelay)
		}
	}
	base := t.policy.BaseDelay
	if base <= 0 {
		base = 200 * time.Millisecond
	}
	maxDelay := t.policy.MaxDelay
	if maxDelay <= 0 {
		maxDelay = 5 * time.Second
	}
	growth := math.Pow(2, float64(attempt-1))
	delay := min(time.Duration(float64(base)*growth), maxDelay)
	// Full jitter: spread retries so concurrent workers do not resynchronise.
	return delay/2 + time.Duration(rand.Int64N(int64(delay/2)+1)) //nolint:gosec // G404: retry jitter needs spread, not unpredictability
}

func retryAfter(resp *http.Response) (time.Duration, bool) {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second, true
	}
	if when, err := http.ParseTime(v); err == nil {
		if d := time.Until(when); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func retryableErr(err error) bool {
	// Context cancellation is the caller's decision, never a transient fault.
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return errors.Is(err, io.EOF)
}

// rateLimitTransport spaces outbound requests so a bulk operation stays a
// courteous neighbour to a community-run site.
type rateLimitTransport struct {
	base     http.RoundTripper
	interval time.Duration

	mu   sync.Mutex
	next time.Time
	now  func() time.Time
}

func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if wait := t.reserve(); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
	return t.base.RoundTrip(req)
}

func (t *rateLimitTransport) reserve() time.Duration {
	if t.interval <= 0 {
		return 0
	}
	nowFn := t.now
	if nowFn == nil {
		nowFn = time.Now
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	now := nowFn()
	if t.next.After(now) {
		wait := t.next.Sub(now)
		t.next = t.next.Add(t.interval)
		return wait
	}
	t.next = now.Add(t.interval)
	return 0
}

// headerTransport stamps every request with the client's identity.
type headerTransport struct {
	base      http.RoundTripper
	userAgent string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.userAgent != "" && req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", t.userAgent)
	}
	return t.base.RoundTrip(req)
}
