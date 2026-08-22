package cache

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"time"
)

// Transport wraps base with the cache. Requests that carry an Authorization
// header are never stored: a shared on-disk cache is the wrong home for
// per-user data.
func (c *Cache) Transport(base http.RoundTripper, mode Mode) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{cache: c, base: base, mode: mode, now: time.Now}
}

type transport struct {
	cache *Cache
	base  http.RoundTripper
	mode  Mode
	now   func() time.Time
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.cacheable(req) {
		if t.mode == ModeOffline {
			return nil, ErrOffline
		}
		resp, err := t.base.RoundTrip(req)
		if err == nil {
			resp.Header.Set(StatusHeader, "bypass")
		}
		return resp, err
	}

	key := keyFor(req)
	stored, loadErr := t.cache.load(key)
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		// A corrupt or unreadable entry is not worth failing the command over.
		stored = nil
	}

	now := t.now()
	if stored != nil && t.mode == ModeDefault && stored.fresh(now) {
		t.cache.hits.Add(1)
		return stored.response(req, "hit"), nil
	}
	if stored != nil && t.mode == ModeOffline {
		t.cache.hits.Add(1)
		return stored.response(req, "hit"), nil
	}
	if t.mode == ModeOffline {
		return nil, ErrOffline
	}

	outbound := req
	if stored != nil {
		outbound = req.Clone(req.Context())
		if stored.ETag != "" {
			outbound.Header.Set("If-None-Match", stored.ETag)
		}
		if stored.LastModified != "" {
			outbound.Header.Set("If-Modified-Since", stored.LastModified)
		}
	}

	resp, err := t.base.RoundTrip(outbound)
	if err != nil {
		if stored != nil {
			// Serving a stale answer beats failing when the network is down.
			t.cache.hits.Add(1)
			return stored.response(req, "stale"), nil
		}
		return nil, err
	}

	if resp.StatusCode == http.StatusNotModified && stored != nil {
		drain(resp)
		stored.FreshUntil = t.cache.freshUntil(resp, now)
		stored.StoredAt = now
		if etag := resp.Header.Get("ETag"); etag != "" {
			stored.ETag = etag
		}
		if lm := resp.Header.Get("Last-Modified"); lm != "" {
			stored.LastModified = lm
		}
		if err := t.cache.store(key, stored); err != nil {
			t.cache.writeFailure.Add(1)
		}
		t.cache.revalidated.Add(1)
		return stored.response(req, "revalidated"), nil
	}

	t.cache.misses.Add(1)
	if resp.StatusCode != http.StatusOK || noStore(resp.Header.Get("Cache-Control")) {
		resp.Header.Set(StatusHeader, "miss")
		return resp, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
	drain(resp)
	if err != nil {
		return nil, err
	}

	e := &entry{
		URL:          req.URL.String(),
		Method:       req.Method,
		Status:       resp.StatusCode,
		Header:       resp.Header.Clone(),
		StoredAt:     now,
		FreshUntil:   t.cache.freshUntil(resp, now),
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		body:         body,
	}
	e.Header.Del(StatusHeader)
	if err := t.cache.store(key, e); err != nil {
		t.cache.writeFailure.Add(1)
	}
	return e.response(req, "miss"), nil
}

func (t *transport) cacheable(req *http.Request) bool {
	if t.mode == ModeBypass {
		return false
	}
	if req.Method != http.MethodGet {
		return false
	}
	if req.Header.Get("Authorization") != "" {
		return false
	}
	return true
}

// response rebuilds an http.Response from a stored entry.
func (e *entry) response(req *http.Request, status string) *http.Response {
	header := e.Header.Clone()
	if header == nil {
		header = http.Header{}
	}
	header.Set(StatusHeader, status)
	return &http.Response{
		Status:        http.StatusText(e.Status),
		StatusCode:    e.Status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(e.body)),
		ContentLength: int64(len(e.body)),
		Request:       req,
	}
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
}
