package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetriesTransientStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		status       int
		wantAttempts int32
	}{
		{"429 is retried", http.StatusTooManyRequests, 3},
		{"503 is retried", http.StatusServiceUnavailable, 3},
		{"502 is retried", http.StatusBadGateway, 3},
		// MarvelCDB uses 500 for "no such record", so retrying only wastes
		// requests that can never succeed.
		{"500 is not retried", http.StatusInternalServerError, 1},
		{"400 is not retried", http.StatusBadRequest, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, `{"error":{"code":1,"message":"nope"}}`)
			}))
			defer srv.Close()

			c, err := New(
				WithBaseURL(srv.URL),
				WithRateLimit(0),
				WithRetryPolicy(RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}),
			)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := c.Packs(context.Background()); err == nil {
				t.Fatal("expected an error")
			}
			if got := attempts.Load(); got != tt.wantAttempts {
				t.Errorf("attempts = %d, want %d", got, tt.wantAttempts)
			}
		})
	}
}

func TestRetrySucceedsAfterTransientFailure(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `[{"code":"core"}]`)
	}))
	defer srv.Close()

	c, err := New(
		WithBaseURL(srv.URL),
		WithRateLimit(0),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	packs, err := c.Packs(context.Background())
	if err != nil {
		t.Fatalf("Packs: %v", err)
	}
	if len(packs) != 1 {
		t.Errorf("len(packs) = %d, want 1", len(packs))
	}
}

// A retried write must resend its body, not an empty one.
func TestRetryReplaysRequestBody(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	bodies := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies <- string(body)
		if attempts.Add(1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"success":true,"msg":1}`)
	}))
	defer srv.Close()

	c, err := New(
		WithBaseURL(srv.URL),
		WithRateLimit(0),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.NewDeck(context.Background(), NewDeckRequest{Hero: "01001a"}); err != nil {
		t.Fatalf("NewDeck: %v", err)
	}
	close(bodies)

	seen := 0
	for body := range bodies {
		seen++
		if body == "" {
			t.Error("a retried request sent an empty body")
		}
	}
	if seen != 2 {
		t.Errorf("saw %d requests, want 2", seen)
	}
}

func TestRetryAfterHeaderIsHonoured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		header string
		want   time.Duration
		ok     bool
	}{
		{"2", 2 * time.Second, true},
		{"0", 0, true},
		{"", 0, false},
		{"not-a-number", 0, false},
	}
	for _, tt := range tests {
		resp := &http.Response{Header: http.Header{}}
		if tt.header != "" {
			resp.Header.Set("Retry-After", tt.header)
		}
		got, ok := retryAfter(resp)
		if ok != tt.ok || got != tt.want {
			t.Errorf("retryAfter(%q) = %v, %v; want %v, %v", tt.header, got, ok, tt.want, tt.ok)
		}
	}
}

func TestRateLimitSpacesRequests(t *testing.T) {
	t.Parallel()

	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count.Add(1)
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	interval := 25 * time.Millisecond
	c, err := New(WithBaseURL(srv.URL), WithRateLimit(interval),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	start := time.Now()
	for range 3 {
		if _, err := c.Packs(context.Background()); err != nil {
			t.Fatalf("Packs: %v", err)
		}
	}
	elapsed := time.Since(start)

	// Three requests at one per interval must take at least two intervals.
	if elapsed < 2*interval {
		t.Errorf("three requests took %v, want at least %v", elapsed, 2*interval)
	}
	if count.Load() != 3 {
		t.Errorf("server saw %d requests, want 3", count.Load())
	}
}

func TestBackoffStaysWithinBounds(t *testing.T) {
	t.Parallel()

	rt := &retryTransport{policy: RetryPolicy{BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second}}
	for attempt := 1; attempt <= 6; attempt++ {
		d := rt.backoff(attempt, nil)
		if d < 0 || d > time.Second {
			t.Errorf("backoff(%d) = %v, want between 0 and 1s", attempt, d)
		}
	}
}
