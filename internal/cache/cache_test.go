package cache

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newCache(t *testing.T, ttl time.Duration) *Cache {
	t.Helper()
	c, err := New(t.TempDir(), ttl)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// get performs a request and returns the cache disposition and the body,
// closing the response before returning either.
func get(t *testing.T, rt http.RoundTripper, url string) (status, body string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.Header.Get(StatusHeader), string(raw)
}

func TestFreshEntryIsServedWithoutTheNetwork(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "max-age=600, public")
		_, _ = io.WriteString(w, "payload")
	}))
	defer srv.Close()

	rt := newCache(t, time.Hour).Transport(http.DefaultTransport, ModeDefault)

	status, body := get(t, rt, srv.URL)
	if body != "payload" || status != "miss" {
		t.Fatalf("first request: body %q status %q", body, status)
	}
	status, body = get(t, rt, srv.URL)
	if body != "payload" || status != "hit" {
		t.Fatalf("second request: body %q status %q", body, status)
	}
	if hits.Load() != 1 {
		t.Errorf("server saw %d requests, want 1", hits.Load())
	}
}

// Revalidation is what keeps a 7 MB card corpus cheap: a stale entry costs a
// 304, not a re-download.
func TestStaleEntryIsRevalidated(t *testing.T) {
	t.Parallel()

	var conditional atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` || r.Header.Get("If-Modified-Since") != "" {
			conditional.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		_, _ = io.WriteString(w, "payload")
	}))
	defer srv.Close()

	// A zero TTL with no max-age means entries go stale immediately here.
	c := newCache(t, time.Nanosecond)
	rt := c.Transport(http.DefaultTransport, ModeDefault)

	if _, body := get(t, rt, srv.URL); body != "payload" {
		t.Fatalf("first body = %q", body)
	}
	status, body := get(t, rt, srv.URL)
	if body != "payload" {
		t.Errorf("revalidated body = %q, want the stored payload", body)
	}
	if status != "revalidated" {
		t.Errorf("status = %q, want revalidated", status)
	}
	if conditional.Load() != 1 {
		t.Errorf("conditional requests = %d, want 1", conditional.Load())
	}

	stats, err := c.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Revalidated != 1 {
		t.Errorf("stats.Revalidated = %d, want 1", stats.Revalidated)
	}
}

func TestStaleEntryServedWhenTheNetworkFails(t *testing.T) {
	t.Parallel()

	var fail atomic.Bool
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if fail.Load() {
			return nil, errors.New("network is down")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("payload")),
			Request:    r,
		}, nil
	})

	rt := newCache(t, time.Nanosecond).Transport(base, ModeDefault)
	if _, body := get(t, rt, "http://example.invalid/cards"); body != "payload" {
		t.Fatalf("priming request failed")
	}

	fail.Store(true)
	status, body := get(t, rt, "http://example.invalid/cards")
	if body != "payload" {
		t.Errorf("body = %q, want the stale payload", body)
	}
	if status != "stale" {
		t.Errorf("status = %q, want stale", status)
	}
}

func TestModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mode       Mode
		wantStatus string
		wantCalls  int32
	}{
		{"default serves the hit", ModeDefault, "hit", 1},
		{"bypass ignores the cache", ModeBypass, "bypass", 2},
		{"refresh revalidates", ModeRefresh, "miss", 2},
		{"offline serves the entry", ModeOffline, "hit", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32
			base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{},
					Body:       io.NopCloser(strings.NewReader("payload")),
					Request:    r,
				}, nil
			})

			c := newCache(t, time.Hour)
			prime := c.Transport(base, ModeDefault)
			get(t, prime, "http://example.invalid/x")

			rt := c.Transport(base, tt.mode)
			status, _ := get(t, rt, "http://example.invalid/x")
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
			if calls.Load() != tt.wantCalls {
				t.Errorf("upstream calls = %d, want %d", calls.Load(), tt.wantCalls)
			}
		})
	}
}

func TestOfflineWithoutAnEntryFails(t *testing.T) {
	t.Parallel()

	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("offline mode must not reach the network")
		return nil, errors.New("unreachable")
	})
	rt := newCache(t, time.Hour).Transport(base, ModeOffline)

	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid/missing", nil)
	resp, err := rt.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrOffline) {
		t.Fatalf("err = %v, want ErrOffline", err)
	}
}

// A shared on-disk cache is the wrong home for per-user data.
func TestAuthenticatedAndNonGetRequestsAreNotCached(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		header string
	}{
		{"authorized GET", http.MethodGet, "Bearer secret"},
		{"POST", http.MethodPost, ""},
		{"DELETE", http.MethodDelete, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newCache(t, time.Hour)
			base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{},
					Body:       io.NopCloser(strings.NewReader("secret payload")),
					Request:    r,
				}, nil
			})
			rt := c.Transport(base, ModeDefault)

			req, _ := http.NewRequest(tt.method, "http://example.invalid/decks", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			resp, err := rt.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()

			stats, err := c.Stats()
			if err != nil {
				t.Fatalf("Stats: %v", err)
			}
			if stats.Entries != 0 {
				t.Errorf("cache holds %d entries; it must hold none", stats.Entries)
			}
		})
	}
}

func TestNonOKResponsesAreNotStored(t *testing.T) {
	t.Parallel()

	c := newCache(t, time.Hour)
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("boom")),
			Request:    r,
		}, nil
	})
	rt := c.Transport(base, ModeDefault)
	get(t, rt, "http://example.invalid/broken")

	stats, _ := c.Stats()
	if stats.Entries != 0 {
		t.Errorf("stored %d entries for a 500; want 0", stats.Entries)
	}
}

func TestNoStoreIsRespected(t *testing.T) {
	t.Parallel()

	c := newCache(t, time.Hour)
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		h := http.Header{}
		h.Set("Cache-Control", "private, no-store")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     h,
			Body:       io.NopCloser(strings.NewReader("payload")),
			Request:    r,
		}, nil
	})
	get(t, c.Transport(base, ModeDefault), "http://example.invalid/private")

	stats, _ := c.Stats()
	if stats.Entries != 0 {
		t.Errorf("stored %d entries for a no-store response; want 0", stats.Entries)
	}
}

func TestClearAndPrune(t *testing.T) {
	t.Parallel()

	c := newCache(t, time.Hour)
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("payload")),
			Request:    r,
		}, nil
	})
	rt := c.Transport(base, ModeDefault)
	for _, path := range []string{"/a", "/b", "/c"} {
		get(t, rt, "http://example.invalid"+path)
	}

	if n, err := c.Prune(time.Now().Add(-time.Hour)); err != nil || n != 0 {
		t.Errorf("Prune with an old cutoff removed %d entries (err %v), want 0", n, err)
	}
	if n, err := c.Prune(time.Now().Add(time.Hour)); err != nil || n != 3 {
		t.Errorf("Prune with a future cutoff removed %d entries (err %v), want 3", n, err)
	}

	for _, path := range []string{"/a", "/b"} {
		get(t, rt, "http://example.invalid"+path)
	}
	n, err := c.Clear()
	if err != nil || n != 2 {
		t.Errorf("Clear removed %d entries (err %v), want 2", n, err)
	}
	stats, _ := c.Stats()
	if stats.Entries != 0 {
		t.Errorf("after Clear the cache holds %d entries", stats.Entries)
	}
}

func TestCorruptEntryIsIgnored(t *testing.T) {
	t.Parallel()

	c := newCache(t, time.Hour)
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("payload")),
			Request:    r,
		}, nil
	})
	rt := c.Transport(base, ModeDefault)
	get(t, rt, "http://example.invalid/x")

	entries, err := os.ReadDir(c.Dir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if err := os.WriteFile(filepath.Join(c.Dir(), e.Name()), []byte("garbage"), 0o600); err != nil {
			t.Fatalf("corrupt entry: %v", err)
		}
	}

	// A corrupt entry must fall back to the network, not fail the command.
	if _, body := get(t, rt, "http://example.invalid/x"); body != "payload" {
		t.Errorf("body = %q, want payload", body)
	}
}

func TestMaxAgeParsing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		header string
		want   time.Duration
		ok     bool
	}{
		{"max-age=600, public", 600 * time.Second, true},
		{"public, max-age=0", 0, true},
		{"no-store", 0, false},
		{"", 0, false},
		{"max-age=abc", 0, false},
	}
	for _, tt := range tests {
		got, ok := maxAge(tt.header)
		if got != tt.want || ok != tt.ok {
			t.Errorf("maxAge(%q) = %v, %v; want %v, %v", tt.header, got, ok, tt.want, tt.ok)
		}
	}
}

func TestNoStoreDetection(t *testing.T) {
	t.Parallel()

	for header, want := range map[string]bool{
		"no-store":            true,
		"private, max-age=60": true,
		"public, max-age=600": false,
		"":                    false,
	} {
		if got := noStore(header); got != want {
			t.Errorf("noStore(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestNewRejectsAnEmptyDirectory(t *testing.T) {
	t.Parallel()
	if _, err := New("", time.Hour); err == nil {
		t.Fatal("expected an error for an empty cache directory")
	}
}
