package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadMissingFileFallsBackToDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BaseURL != Default().BaseURL {
		t.Errorf("BaseURL = %q, want the default", cfg.BaseURL)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")

	want := Default()
	want.Output = "json"
	want.CacheTTL = Duration(6 * time.Hour)
	want.Locale = "de"
	want.MaxRetries = 2

	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// The file holds preferences, but a tight mode is still the right default.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config file mode = %o, want 600", perm)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Output != "json" || got.Locale != "de" || got.MaxRetries != 2 {
		t.Errorf("round-tripped config = %+v", got)
	}
	if got.CacheTTL.Duration() != 6*time.Hour {
		t.Errorf("CacheTTL = %v, want 6h", got.CacheTTL.Duration())
	}
}

func TestSavedFileCarriesNoSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Default().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	body := strings.ToLower(string(data))
	for _, forbidden := range []string{"client_secret", "access_token", "refresh_token", "password"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("config file contains %q; credentials belong in the keyring", forbidden)
		}
	}
}

func TestEnvironmentOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := Default()
	cfg.Output = "table"
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	t.Setenv(EnvOutput, "json")
	t.Setenv(EnvLocale, "fr")
	t.Setenv(EnvCacheTTL, "15m")
	t.Setenv(EnvNoColor, "1")

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Output != "json" {
		t.Errorf("Output = %q, want json from the environment", got.Output)
	}
	if got.Locale != "fr" {
		t.Errorf("Locale = %q, want fr", got.Locale)
	}
	if got.CacheTTL.Duration() != 15*time.Minute {
		t.Errorf("CacheTTL = %v, want 15m", got.CacheTTL.Duration())
	}
	if got.Color != "never" {
		t.Errorf("Color = %q, want never when NO_COLOR is set", got.Color)
	}
}

func TestLoadRejectsInvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("base_url: [unclosed"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"defaults are valid", func(*Config) {}, false},
		{"empty base url", func(c *Config) { c.BaseURL = "" }, true},
		{"base url without a scheme", func(c *Config) { c.BaseURL = "marvelcdb.com" }, true},
		{"bad color", func(c *Config) { c.Color = "rainbow" }, true},
		{"zero retries", func(c *Config) { c.MaxRetries = 0 }, true},
		{"negative ttl", func(c *Config) { c.CacheTTL = Duration(-time.Second) }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			tt.mutate(&cfg)
			if err := cfg.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetAndSetEveryField(t *testing.T) {
	t.Parallel()

	cfg := Default()
	for _, key := range Fields() {
		if _, err := cfg.Get(key); err != nil {
			t.Errorf("Get(%q) = %v", key, err)
		}
	}
	if _, err := cfg.Get("nonsense"); err == nil {
		t.Error("Get on an unknown key should fail")
	}

	tests := []struct {
		key, value string
		wantErr    bool
	}{
		{"base_url", "https://example.com", false},
		{"locale", "de", false},
		{"output", "json", false},
		{"color", "never", false},
		{"cache_ttl", "12h", false},
		{"rate_limit", "250ms", false},
		{"timeout", "30s", false},
		{"max_retries", "2", false},
		{"cache_ttl", "twelve hours", true},
		{"max_retries", "many", true},
		{"max_retries", "0", true},
		{"color", "sparkly", true},
		{"nonsense", "x", true},
	}
	for _, tt := range tests {
		c := Default()
		err := c.Set(tt.key, tt.value)
		if (err != nil) != tt.wantErr {
			t.Errorf("Set(%q, %q) = %v, wantErr %v", tt.key, tt.value, err, tt.wantErr)
			continue
		}
		if err == nil {
			got, _ := c.Get(tt.key)
			if got == "" && tt.value != "" {
				t.Errorf("Set(%q, %q) then Get returned empty", tt.key, tt.value)
			}
		}
	}
}

func TestDurationYAML(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("cache_ttl: 90m\nrate_limit: 3\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CacheTTL.Duration() != 90*time.Minute {
		t.Errorf("CacheTTL = %v, want 90m", cfg.CacheTTL.Duration())
	}
	// A bare number is read as seconds, which is what people expect.
	if cfg.RateLimit.Duration() != 3*time.Second {
		t.Errorf("RateLimit = %v, want 3s", cfg.RateLimit.Duration())
	}

	if err := os.WriteFile(path, []byte("cache_ttl: soon\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Error("an unparsable duration should fail the load")
	}
}

func TestDefaultPathHonoursTheEnvironment(t *testing.T) {
	t.Setenv(EnvConfig, "/custom/marvelcdb.yaml")
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if got != "/custom/marvelcdb.yaml" {
		t.Errorf("DefaultPath = %q", got)
	}
}

func TestDefaultCacheDirHonoursTheEnvironment(t *testing.T) {
	t.Setenv(EnvCacheDir, "/custom/cache")
	if got := DefaultCacheDir(); got != "/custom/cache" {
		t.Errorf("DefaultCacheDir = %q", got)
	}
}

func TestSaveRequiresAPath(t *testing.T) {
	t.Parallel()
	if err := Default().Save(""); err == nil {
		t.Fatal("expected an error for an empty path")
	}
}
