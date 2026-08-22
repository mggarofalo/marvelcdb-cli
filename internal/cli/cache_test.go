package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCacheCommands(t *testing.T) {
	srv := newFakeMarvelCDB(t)

	dir := t.TempDir()
	t.Setenv("MARVELCDB_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("MARVELCDB_CACHE_DIR", filepath.Join(dir, "cache"))

	warm, err := runCachedCommand(t, srv, "cache", "warm")
	if err != nil {
		t.Fatalf("cache warm: %v", err)
	}
	if !strings.Contains(warm, "cards") {
		t.Errorf("cache warm output:\n%s", warm)
	}

	info, err := runCachedCommand(t, srv, "cache", "info")
	if err != nil {
		t.Fatalf("cache info: %v", err)
	}
	if !strings.Contains(info, "entries") || strings.Contains(info, "entries       0") {
		t.Errorf("cache info should report a warmed cache:\n%s", info)
	}

	if _, err := runCachedCommand(t, srv, "cache", "prune", "--older-than", "1000h"); err != nil {
		t.Fatalf("cache prune: %v", err)
	}
	if _, err := runCachedCommand(t, srv, "cache", "clear"); err != nil {
		t.Fatalf("cache clear: %v", err)
	}

	after, err := runCachedCommand(t, srv, "cache", "info")
	if err != nil {
		t.Fatalf("cache info: %v", err)
	}
	if !strings.Contains(after, "entries") {
		t.Errorf("cache info after clear:\n%s", after)
	}
}

// runCachedCommand shares one cache directory across calls, which the plain
// helper deliberately does not.
func runCachedCommand(t *testing.T, srv *fakeMarvelCDB, args ...string) (string, error) {
	t.Helper()

	root := NewRootCommand()
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"--base-url", srv.URL, "--rate-limit", "0"}, args...))

	stdout, restore := captureStdout(t)
	err := root.ExecuteContext(t.Context())
	return restore(stdout) + out.String(), err
}

func TestCacheCommandsWithCachingDisabled(t *testing.T) {
	srv := newFakeMarvelCDB(t)
	for _, sub := range []string{"info", "clear", "prune"} {
		_, _, err := run(t, srv, "cache", sub, "--no-cache")
		if err == nil || !strings.Contains(err.Error(), "caching is disabled") {
			t.Errorf("cache %s --no-cache = %v, want a clear explanation", sub, err)
		}
	}
}

func TestFlagCompletions(t *testing.T) {
	srv := newFakeMarvelCDB(t)

	dir := t.TempDir()
	t.Setenv("MARVELCDB_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("MARVELCDB_CACHE_DIR", filepath.Join(dir, "cache"))

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"pack codes", []string{"__complete", "cards", "list", "--pack", ""}, "core"},
		{"faction codes", []string{"__complete", "cards", "list", "--faction", ""}, "leadership"},
		{"type codes", []string{"__complete", "cards", "list", "--type", ""}, "ally"},
		{"sort fields", []string{"__complete", "cards", "list", "--sort", ""}, "name"},
		{"output formats", []string{"__complete", "--output", ""}, "json"},
		{"locales", []string{"__complete", "--locale", ""}, "de"},
		{"config settings", []string{"__complete", "config", "get", ""}, "cache_ttl"},
		{"card codes", []string{"__complete", "cards", "get", ""}, "01067"},
		{"pack codes as arguments", []string{"__complete", "packs", "get", ""}, "core"},
		{"hero codes", []string{"__complete", "mydecks", "new", "--hero", ""}, ""},
		{"aspects", []string{"__complete", "mydecks", "new", "--aspect", ""}, "justice"},
		{"problems", []string{"__complete", "mydecks", "save", "--problem", ""}, "too_few_cards"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := runCachedCommand(t, srv, tt.args...)
			if err != nil {
				t.Fatalf("completion: %v", err)
			}
			if tt.want != "" && !strings.Contains(got, tt.want) {
				t.Errorf("completions missing %q:\n%s", tt.want, got)
			}
			if srv.requests.Load() == 0 && strings.Contains(strings.Join(tt.args, " "), "cards") {
				t.Error("the completion did not reach the test server; it may have escaped to the live site")
			}
		})
	}
}

func TestCompletionScriptsGenerate(t *testing.T) {
	t.Parallel()

	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			t.Parallel()
			root := NewRootCommand()
			var out strings.Builder
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs([]string{"completion", shell})
			if err := root.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("completion %s: %v", shell, err)
			}
			if out.Len() == 0 {
				t.Errorf("completion %s produced nothing", shell)
			}
		})
	}
}

func TestEveryCommandHasHelp(t *testing.T) {
	t.Parallel()

	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Name() != "marvelcdb" && c.Runnable() && c.Long == "" && c.Short == "" {
			t.Errorf("%s has no help text", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(NewRootCommand())
}
