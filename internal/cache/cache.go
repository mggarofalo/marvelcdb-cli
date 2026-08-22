// Package cache gives the CLI an on-disk HTTP cache.
//
// MarvelCDB serves the card corpus as a single ~7 MB document and supports
// conditional requests, so caching is the difference between a CLI that feels
// instant and one that re-downloads the game on every invocation. Entries are
// revalidated with If-None-Match / If-Modified-Since once they go stale, which
// costs one 304 rather than a fresh download.
package cache

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Mode selects how the cache participates in a request.
type Mode int

const (
	// ModeDefault serves fresh entries, revalidates stale ones.
	ModeDefault Mode = iota
	// ModeBypass neither reads nor writes the cache.
	ModeBypass
	// ModeRefresh ignores freshness but still uses conditional requests, so an
	// unchanged resource costs a 304 instead of a full download.
	ModeRefresh
	// ModeOffline never touches the network and fails when an entry is missing.
	ModeOffline
)

// ErrOffline is returned when offline mode has no entry to serve.
var ErrOffline = errors.New("offline: no cached response for this request")

// StatusHeader carries the cache disposition on responses the cache handled.
// Values are "hit", "revalidated", "miss", "stale" and "bypass".
const StatusHeader = "X-Marvelcdb-Cache"

const (
	entrySuffix = ".entry"
	// bodyLimit bounds what the cache is willing to store for one response.
	bodyLimit = 128 << 20
)

// Cache stores HTTP responses under a directory.
type Cache struct {
	dir string
	ttl time.Duration

	hits         atomic.Int64
	misses       atomic.Int64
	revalidated  atomic.Int64
	writeFailure atomic.Int64
}

// New opens (and creates) a cache rooted at dir. A ttl of zero falls back to
// the response's own max-age, or one hour when the response does not say.
func New(dir string, ttl time.Duration) (*Cache, error) {
	if dir == "" {
		return nil, errors.New("cache directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create cache directory %s: %w", dir, err)
	}
	return &Cache{dir: dir, ttl: ttl}, nil
}

// Dir reports where entries are stored.
func (c *Cache) Dir() string { return c.dir }

// Stats counts what this process did with the cache, plus what is on disk.
type Stats struct {
	Entries     int           `json:"entries"`
	Bytes       int64         `json:"bytes"`
	Oldest      time.Time     `json:"oldest,omitzero"`
	Newest      time.Time     `json:"newest,omitzero"`
	Hits        int64         `json:"hits"`
	Misses      int64         `json:"misses"`
	Revalidated int64         `json:"revalidated"`
	Dir         string        `json:"dir"`
	TTL         time.Duration `json:"-"`
}

// Stats inspects the cache directory.
func (c *Cache) Stats() (Stats, error) {
	s := Stats{
		Dir:         c.dir,
		TTL:         c.ttl,
		Hits:        c.hits.Load(),
		Misses:      c.misses.Load(),
		Revalidated: c.revalidated.Load(),
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, fmt.Errorf("read cache directory: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), entrySuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		s.Entries++
		s.Bytes += info.Size()
		mod := info.ModTime()
		if s.Oldest.IsZero() || mod.Before(s.Oldest) {
			s.Oldest = mod
		}
		if mod.After(s.Newest) {
			s.Newest = mod
		}
	}
	return s, nil
}

// Clear deletes every entry and reports how many were removed.
func (c *Cache) Clear() (int, error) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read cache directory: %w", err)
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), entrySuffix) {
			continue
		}
		if err := os.Remove(filepath.Join(c.dir, e.Name())); err != nil {
			return removed, fmt.Errorf("remove cache entry: %w", err)
		}
		removed++
	}
	return removed, nil
}

// Prune deletes entries last written before cutoff.
func (c *Cache) Prune(cutoff time.Time) (int, error) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read cache directory: %w", err)
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), entrySuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(c.dir, e.Name())); err != nil {
			return removed, fmt.Errorf("remove cache entry: %w", err)
		}
		removed++
	}
	return removed, nil
}

// entry is the on-disk record: one JSON header line, then the raw body.
type entry struct {
	URL          string      `json:"url"`
	Method       string      `json:"method"`
	Status       int         `json:"status"`
	Header       http.Header `json:"header"`
	StoredAt     time.Time   `json:"stored_at"`
	FreshUntil   time.Time   `json:"fresh_until"`
	ETag         string      `json:"etag,omitempty"`
	LastModified string      `json:"last_modified,omitempty"`
	body         []byte
}

func (e *entry) fresh(now time.Time) bool { return now.Before(e.FreshUntil) }

func keyFor(req *http.Request) string {
	sum := sha256.Sum256([]byte(req.Method + " " + req.URL.String()))
	return hex.EncodeToString(sum[:])
}

func (c *Cache) path(key string) string { return filepath.Join(c.dir, key+entrySuffix) }

func (c *Cache) load(key string) (*entry, error) {
	f, err := os.Open(c.path(key)) //nolint:gosec // G703: key is a hex SHA-256 digest computed here, not user input
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReader(f)
	header, err := r.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read cache header: %w", err)
	}
	var e entry
	if err := json.Unmarshal(bytes.TrimSpace(header), &e); err != nil {
		return nil, fmt.Errorf("decode cache header: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(r, bodyLimit))
	if err != nil {
		return nil, fmt.Errorf("read cache body: %w", err)
	}
	e.body = body
	return &e, nil
}

func (c *Cache) store(key string, e *entry) error {
	header, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode cache header: %w", err)
	}
	tmp, err := os.CreateTemp(c.dir, "tmp-*")
	if err != nil {
		return fmt.Errorf("create temp cache file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() //nolint:gosec // G703: tmpName came from os.CreateTemp in our own directory

	w := bufio.NewWriter(tmp)
	if _, err := w.Write(append(header, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write cache header: %w", err)
	}
	if _, err := w.Write(e.body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write cache body: %w", err)
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("flush cache entry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close cache entry: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil { //nolint:gosec // G703: tmpName comes from os.CreateTemp in our own directory
		return fmt.Errorf("chmod cache entry: %w", err)
	}
	// Rename is atomic within a directory, so a reader never sees a half entry.
	if err := os.Rename(tmpName, c.path(key)); err != nil { //nolint:gosec // G703: both paths are ours
		return fmt.Errorf("install cache entry: %w", err)
	}
	return nil
}

// freshUntil decides how long a stored response may be served without asking.
// A configured TTL wins over the server's max-age, because MarvelCDB's ten
// minutes is tuned for a browser, not for a CLI that runs in a loop.
func (c *Cache) freshUntil(resp *http.Response, now time.Time) time.Time {
	if c.ttl > 0 {
		return now.Add(c.ttl)
	}
	if d, ok := maxAge(resp.Header.Get("Cache-Control")); ok {
		return now.Add(d)
	}
	return now.Add(time.Hour)
}

func maxAge(cacheControl string) (time.Duration, bool) {
	for _, part := range strings.Split(cacheControl, ",") {
		part = strings.TrimSpace(part)
		value, ok := strings.CutPrefix(part, "max-age=")
		if !ok {
			continue
		}
		secs, err := strconv.Atoi(value)
		if err != nil || secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	return 0, false
}

func noStore(cacheControl string) bool {
	for _, part := range strings.Split(cacheControl, ",") {
		switch strings.TrimSpace(part) {
		case "no-store", "private":
			return true
		}
	}
	return false
}
