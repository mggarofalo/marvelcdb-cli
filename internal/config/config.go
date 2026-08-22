// Package config handles the CLI's persistent settings.
//
// The config file holds preferences only. Credentials live in the OS keyring
// (see internal/auth), so the file is safe to read, diff, and check in.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// EnvConfig names an explicit config file path.
const EnvConfig = "MARVELCDB_CONFIG"

// Environment overrides for individual settings.
const (
	EnvBaseURL   = "MARVELCDB_BASE_URL"
	EnvLocale    = "MARVELCDB_LOCALE"
	EnvOutput    = "MARVELCDB_OUTPUT"
	EnvCacheDir  = "MARVELCDB_CACHE_DIR"
	EnvCacheTTL  = "MARVELCDB_CACHE_TTL"
	EnvNoCache   = "MARVELCDB_NO_CACHE"
	EnvUserAgent = "MARVELCDB_USER_AGENT"
	EnvNoColor   = "NO_COLOR"
)

// Duration is a time.Duration that reads and writes as "30m" in YAML.
type Duration time.Duration

// UnmarshalYAML parses "30m", and also a bare number, which it reads as
// seconds because that is what people mean when they write it.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	if parsed, err := time.ParseDuration(s); err == nil {
		*d = Duration(parsed)
		return nil
	}
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid duration %q: expected a value like 30m or a whole number of seconds", s)
	}
	*d = Duration(time.Duration(secs) * time.Second)
	return nil
}

// MarshalYAML writes the duration as a string.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Duration converts back to time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// Config is the full set of persisted settings.
type Config struct {
	// BaseURL is the MarvelCDB origin.
	BaseURL string `yaml:"base_url" json:"base_url"`
	// Locale selects a translation subdomain: en, de, es, fr, it, ko.
	Locale string `yaml:"locale" json:"locale"`
	// Output is the default renderer: table, json, yaml, csv, tsv or ids.
	Output string `yaml:"output" json:"output"`
	// Color is auto, always or never.
	Color string `yaml:"color" json:"color"`
	// CacheDir holds the on-disk HTTP cache.
	CacheDir string `yaml:"cache_dir" json:"cache_dir"`
	// CacheTTL is how long a cached response is served without revalidating.
	CacheTTL Duration `yaml:"cache_ttl" json:"cache_ttl"`
	// RateLimit is the minimum gap between outbound requests.
	RateLimit Duration `yaml:"rate_limit" json:"rate_limit"`
	// Timeout bounds a single request including its retries.
	Timeout Duration `yaml:"timeout" json:"timeout"`
	// MaxRetries counts total attempts, so 1 disables retrying.
	MaxRetries int `yaml:"max_retries" json:"max_retries"`
	// UserAgent identifies this client to MarvelCDB.
	UserAgent string `yaml:"user_agent" json:"user_agent"`
	// RedirectURI is the OAuth2 callback registered for your client.
	RedirectURI string `yaml:"redirect_uri" json:"redirect_uri"`
	// KeyringService is the service name credentials are filed under.
	KeyringService string `yaml:"keyring_service" json:"keyring_service"`
}

// Default returns the built-in settings.
func Default() Config {
	return Config{
		BaseURL:        "https://marvelcdb.com",
		Locale:         "en",
		Output:         "table",
		Color:          "auto",
		CacheDir:       DefaultCacheDir(),
		CacheTTL:       Duration(time.Hour),
		RateLimit:      Duration(100 * time.Millisecond),
		Timeout:        Duration(60 * time.Second),
		MaxRetries:     4,
		UserAgent:      "",
		RedirectURI:    "http://127.0.0.1:8765/callback",
		KeyringService: "marvelcdb-cli",
	}
}

// DefaultCacheDir is the per-user cache location for this platform.
func DefaultCacheDir() string {
	if v := strings.TrimSpace(os.Getenv(EnvCacheDir)); v != "" {
		return v
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "marvelcdb-cli")
	}
	return filepath.Join(base, "marvelcdb-cli", "http")
}

// DefaultPath is where the config file lives unless MARVELCDB_CONFIG says
// otherwise.
func DefaultPath() (string, error) {
	if v := strings.TrimSpace(os.Getenv(EnvConfig)); v != "" {
		return v, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config directory: %w", err)
	}
	return filepath.Join(base, "marvelcdb-cli", "config.yaml"), nil
}

// Load reads path, falling back to defaults when the file does not exist.
// Environment overrides are applied last.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // path comes from the user
		switch {
		case err == nil:
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return cfg, fmt.Errorf("parse config %s: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
			// A missing config file is the normal case, not a problem.
		default:
			return cfg, fmt.Errorf("read config %s: %w", path, err)
		}
	}
	cfg.applyEnv()
	return cfg, cfg.Validate()
}

func (c *Config) applyEnv() {
	if v := strings.TrimSpace(os.Getenv(EnvBaseURL)); v != "" {
		c.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvLocale)); v != "" {
		c.Locale = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvOutput)); v != "" {
		c.Output = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvCacheDir)); v != "" {
		c.CacheDir = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvUserAgent)); v != "" {
		c.UserAgent = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvCacheTTL)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.CacheTTL = Duration(d)
		}
	}
	if os.Getenv(EnvNoColor) != "" {
		c.Color = "never"
	}
}

// Validate rejects settings that would fail later in a confusing way.
func (c Config) Validate() error {
	if c.BaseURL == "" {
		return errors.New("base_url must not be empty")
	}
	if !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
		return fmt.Errorf("base_url %q must start with http:// or https://", c.BaseURL)
	}
	switch c.Color {
	case "", "auto", "always", "never":
	default:
		return fmt.Errorf("color %q must be auto, always or never", c.Color)
	}
	if c.MaxRetries < 1 {
		return fmt.Errorf("max_retries must be at least 1, got %d", c.MaxRetries)
	}
	if c.Timeout.Duration() < 0 || c.CacheTTL.Duration() < 0 || c.RateLimit.Duration() < 0 {
		return errors.New("durations must not be negative")
	}
	return nil
}

// Save writes the config to path, creating parent directories.
func (c Config) Save(path string) error {
	if path == "" {
		return errors.New("config path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	header := "# marvelcdb-cli configuration.\n" +
		"# Credentials are NOT stored here; they live in the OS keyring.\n"
	if err := os.WriteFile(path, append([]byte(header), data...), 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

// Fields lists the settable keys, for `config set` and its completions.
func Fields() []string {
	return []string{
		"base_url", "locale", "output", "color", "cache_dir", "cache_ttl",
		"rate_limit", "timeout", "max_retries", "user_agent", "redirect_uri",
		"keyring_service",
	}
}

// Get reads one field by its config-file key.
func (c Config) Get(key string) (string, error) {
	switch key {
	case "base_url":
		return c.BaseURL, nil
	case "locale":
		return c.Locale, nil
	case "output":
		return c.Output, nil
	case "color":
		return c.Color, nil
	case "cache_dir":
		return c.CacheDir, nil
	case "cache_ttl":
		return c.CacheTTL.Duration().String(), nil
	case "rate_limit":
		return c.RateLimit.Duration().String(), nil
	case "timeout":
		return c.Timeout.Duration().String(), nil
	case "max_retries":
		return strconv.Itoa(c.MaxRetries), nil
	case "user_agent":
		return c.UserAgent, nil
	case "redirect_uri":
		return c.RedirectURI, nil
	case "keyring_service":
		return c.KeyringService, nil
	default:
		return "", fmt.Errorf("unknown setting %q; known settings: %s", key, strings.Join(Fields(), ", "))
	}
}

// Set writes one field by its config-file key.
func (c *Config) Set(key, value string) error {
	switch key {
	case "base_url":
		c.BaseURL = value
	case "locale":
		c.Locale = value
	case "output":
		c.Output = value
	case "color":
		c.Color = value
	case "cache_dir":
		c.CacheDir = value
	case "cache_ttl", "rate_limit", "timeout":
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid duration %q for %s: %w", value, key, err)
		}
		switch key {
		case "cache_ttl":
			c.CacheTTL = Duration(d)
		case "rate_limit":
			c.RateLimit = Duration(d)
		case "timeout":
			c.Timeout = Duration(d)
		}
	case "max_retries":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid integer %q for max_retries: %w", value, err)
		}
		c.MaxRetries = n
	case "user_agent":
		c.UserAgent = value
	case "redirect_uri":
		c.RedirectURI = value
	case "keyring_service":
		c.KeyringService = value
	default:
		return fmt.Errorf("unknown setting %q; known settings: %s", key, strings.Join(Fields(), ", "))
	}
	return c.Validate()
}
