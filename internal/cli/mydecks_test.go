package cli

import (
	"context"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// signedIn points the credential store at the environment, which is the
// documented escape hatch for machines with no keyring — and exactly what a
// test needs.
func signedIn(t *testing.T) {
	t.Helper()
	t.Setenv("MARVELCDB_CLIENT_ID", "1_test")
	t.Setenv("MARVELCDB_CLIENT_SECRET", "s3cret")
	t.Setenv("MARVELCDB_ACCESS_TOKEN", "test-token")
}

func TestMyDecksEndToEnd(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		contains []string
		wantForm map[string]string
	}{
		{
			name:     "list",
			args:     []string{"mydecks", "list"},
			contains: []string{"My deck", "Spider-Man"},
		},
		{
			name:     "get",
			args:     []string{"mydecks", "get", "1"},
			contains: []string{"My deck"},
		},
		{
			name:     "new",
			args:     []string{"mydecks", "new", "--hero", "01001a", "--name", "Webhead", "--aspect", "justice"},
			contains: []string{"created deck"},
			wantForm: map[string]string{"hero": "01001a", "name": "Webhead", "meta": `{"aspect":"justice"}`},
		},
		{
			name:     "save",
			args:     []string{"mydecks", "save", "1", "--slot", "01067=2", "--problem", "too_few_cards"},
			contains: []string{"saved deck"},
			wantForm: map[string]string{"slots": `{"01067":2}`, "problem": "too_few_cards"},
		},
		{
			name:     "delete with --yes",
			args:     []string{"mydecks", "delete", "1", "--yes"},
			contains: []string{"deleted"},
		},
		{
			name:     "publish with --yes",
			args:     []string{"mydecks", "publish", "1", "--yes", "--description", "notes"},
			contains: []string{"published decklist"},
			wantForm: map[string]string{"description_md": "notes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signedIn(t)
			srv := newFakeMarvelCDB(t)
			got, err := runCaptured(t, srv, tt.args...)
			if err != nil {
				t.Fatalf("run(%v) = %v\n%s", tt.args, err, got)
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q:\n%s", want, got)
				}
			}
			if tt.wantForm == nil {
				return
			}
			raw, _ := srv.lastForm.Load().(string)
			form, err := url.ParseQuery(raw)
			if err != nil {
				t.Fatalf("parse recorded form %q: %v", raw, err)
			}
			for key, want := range tt.wantForm {
				if got := form.Get(key); got != want {
					t.Errorf("form[%q] = %q, want %q", key, got, want)
				}
			}
		})
	}
}

// A destructive command must not act on an unattended terminal without --yes.
func TestDeleteWithoutConfirmationDoesNothing(t *testing.T) {
	signedIn(t)
	srv := newFakeMarvelCDB(t)

	dir := t.TempDir()
	t.Setenv("MARVELCDB_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("MARVELCDB_CACHE_DIR", filepath.Join(dir, "cache"))

	root := NewRootCommand()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	// Empty stdin stands in for a user who just presses return.
	root.SetIn(strings.NewReader("\n"))
	root.SetArgs([]string{"--base-url", srv.URL, "--rate-limit", "0", "mydecks", "delete", "1"})

	stdout, restore := captureStdout(t)
	err := root.ExecuteContext(context.Background())
	restore(stdout)

	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v, want a cancellation", err)
	}
	if raw, _ := srv.lastForm.Load().(string); raw != "" {
		t.Errorf("a write reached the server despite the cancellation: %q", raw)
	}
}

func TestSaveDryRunSendsNothing(t *testing.T) {
	signedIn(t)
	srv := newFakeMarvelCDB(t)

	got, err := runCaptured(t, srv, "mydecks", "save", "1", "--slot", "01067=2", "--dry-run")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "01067") && !strings.Contains(got, "1 distinct cards") {
		t.Errorf("dry run should describe the request, got:\n%s", got)
	}
	if raw, _ := srv.lastForm.Load().(string); raw != "" {
		t.Errorf("--dry-run sent a request: %q", raw)
	}
}

func TestSaveRequiresSlots(t *testing.T) {
	signedIn(t)
	srv := newFakeMarvelCDB(t)

	_, _, err := run(t, srv, "mydecks", "save", "1")
	if err == nil || !strings.Contains(err.Error(), "slots are required") {
		t.Fatalf("err = %v, want a missing-slots error", err)
	}
}

func TestAuthStatusReflectsTheEnvironment(t *testing.T) {
	signedIn(t)
	srv := newFakeMarvelCDB(t)

	got, err := runCaptured(t, srv, "auth", "status")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "1_test") {
		t.Errorf("status should show the client id:\n%s", got)
	}
	if strings.Contains(got, "s3cret") || strings.Contains(got, "test-token") {
		t.Errorf("status leaked a secret:\n%s", got)
	}
}

func TestAuthTokenPrintsTheAccessToken(t *testing.T) {
	signedIn(t)
	srv := newFakeMarvelCDB(t)

	got, err := runCaptured(t, srv, "auth", "token")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.TrimSpace(got) != "test-token" {
		t.Errorf("auth token printed %q", strings.TrimSpace(got))
	}
}

func TestRedactKeepsOnlyATail(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"", "-"},
		{"abcd", "****"},
		{"abcdefgh", "****efgh"},
	}
	for _, tt := range tests {
		if got := redact(tt.in); got != tt.want {
			t.Errorf("redact(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
