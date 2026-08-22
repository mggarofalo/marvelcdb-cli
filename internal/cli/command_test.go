package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

// fakeMarvelCDB stands in for the real site across the whole command tree.
type fakeMarvelCDB struct {
	*httptest.Server
	requests atomic.Int32
	// lastForm records the body of the most recent write.
	lastForm atomic.Value
}

func newFakeMarvelCDB(t *testing.T) *fakeMarvelCDB {
	t.Helper()
	f := &fakeMarvelCDB{}
	mux := http.NewServeMux()

	json := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "max-age=600, public")
			_, _ = io.WriteString(w, body)
		}
	}

	mux.HandleFunc("/api/public/packs/", json(`[
		{"code":"core","name":"Core Set","position":1,"available":"2019-11-01","known":216,"total":355,"id":1},
		{"code":"gob","name":"The Green Goblin","position":2,"available":"2019-12-20","known":61,"total":78,"id":2}
	]`))
	mux.HandleFunc("/api/public/factions/", json(`[
		{"code":"leadership","name":"Leadership","is_primary":true},
		{"code":"encounter","name":"Encounter","is_primary":false}
	]`))
	mux.HandleFunc("/api/public/cards/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "core.json") {
			json(`[{"code":"01067","name":"Maria Hill","type_code":"ally","type_name":"Ally","faction_code":"leadership","faction_name":"Leadership","pack_code":"core","pack_name":"Core Set","cost":2}]`)(w, r)
			return
		}
		body := `[
			{"code":"01067","name":"Maria Hill","type_code":"ally","type_name":"Ally","faction_code":"leadership","faction_name":"Leadership","pack_code":"core","pack_name":"Core Set","cost":2,"traits":"S.H.I.E.L.D."},
			{"code":"01086","name":"First Aid","type_code":"event","type_name":"Event","faction_code":"protection","faction_name":"Protection","pack_code":"core","pack_name":"Core Set","cost":1,"text":"Heal 5 damage."}
		]`
		if r.URL.Query().Get("encounter") == "1" {
			body = `[
				{"code":"01067","name":"Maria Hill","type_code":"ally","type_name":"Ally","faction_code":"leadership","faction_name":"Leadership","pack_code":"core","pack_name":"Core Set","cost":2,"traits":"S.H.I.E.L.D."},
				{"code":"01086","name":"First Aid","type_code":"event","type_name":"Event","faction_code":"protection","faction_name":"Protection","pack_code":"core","pack_name":"Core Set","cost":1,"text":"Heal 5 damage."},
				{"code":"01130","name":"Rhino","type_code":"villain","type_name":"Villain","faction_code":"encounter","pack_code":"core","pack_name":"Core Set"}
			]`
		}
		json(body)(w, r)
	})
	mux.HandleFunc("/api/public/card/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "01067.json") {
			json(`{"code":"01067","name":"Maria Hill","type_name":"Ally","faction_name":"Leadership","pack_name":"Core Set","cost":2,"imagesrc":"/bundles/cards/01067.png","text":"<b>Response</b>: draw a card."}`)(w, r)
			return
		}
		// MarvelCDB's own behaviour for an unknown code.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":500,"message":"Internal Server Error"}}`)
	})
	mux.HandleFunc("/api/public/faq/", json(`[{"code":"01067","html":"<p><strong>Q</strong>: Really?</p>"}]`))
	mux.HandleFunc("/api/public/taboos/", json(`[]`))
	mux.HandleFunc("/api/public/decklist/", json(`{"id":35109,"name":"Wolverine aggro","hero_code":"35001a","hero_name":"Wolverine","meta":"{\"aspect\":\"aggression\"}","slots":{"01067":1,"01086":2},"date_update":"2024-01-21T16:02:07+00:00"}`))
	mux.HandleFunc("/api/public/decklists/by_date/", json(`[{"id":35109,"name":"Wolverine aggro","hero_name":"Wolverine","slots":{"01067":1}}]`))
	mux.HandleFunc("/api/public/decklists/popular/", json(`[{"decklist":{"id":65081,"name":"Popular deck","hero_name":"Daredevil","slots":{"01067":1}},"hero_meta":{"colors":["#fff"]}}]`))
	mux.HandleFunc("/api/public/deck/", json(`{"id":35109,"name":"Shared deck","hero_name":"Doctor Strange","slots":{"01067":1}}`))

	mux.HandleFunc("/api/oauth2/decks", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json(`[{"id":1,"name":"My deck","hero_name":"Spider-Man","slots":{"01067":1}}]`)(w, r)
	})
	mux.HandleFunc("/api/oauth2/deck/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet {
			json(`{"id":1,"name":"My deck","hero_code":"01001a","hero_name":"Spider-Man","slots":{"01067":1}}`)(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.lastForm.Store(string(body))
		json(`{"success":true,"msg":1}`)(w, r)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	})

	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.Close)
	// A backstop: no test may reach the real marvelcdb.com, whatever path
	// through the flag machinery it takes.
	t.Setenv("MARVELCDB_BASE_URL", f.URL)
	return f
}

// run executes the command tree with an isolated config, cache and keyring.
func run(t *testing.T, srv *fakeMarvelCDB, args ...string) (string, string, error) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("MARVELCDB_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("MARVELCDB_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("NO_COLOR", "1")

	root := NewRootCommand()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(""))

	full := append([]string{"--base-url", srv.URL, "--rate-limit", "0"}, args...)
	root.SetArgs(full)

	err := root.ExecuteContext(context.Background())
	return out.String(), errBuf.String(), err
}

// The printer writes to os.Stdout, so end-to-end assertions capture it.
func runCaptured(t *testing.T, srv *fakeMarvelCDB, args ...string) (string, error) {
	t.Helper()

	stdout, restore := captureStdout(t)
	out, _, err := run(t, srv, args...)
	// Some commands write through the printer to os.Stdout, others through
	// cobra's output writer; assertions should not have to care which.
	return restore(stdout) + out, err
}

func TestPublicCommandsEndToEnd(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		contains []string
		absent   []string
	}{
		{"packs list", []string{"packs", "list"}, []string{"core", "Core Set", "2019-11-01"}, nil},
		{"packs get", []string{"packs", "get", "gob"}, []string{"The Green Goblin"}, []string{"Core Set"}},
		{"factions list", []string{"factions", "list"}, []string{"leadership", "Leadership"}, nil},
		{"factions primary only", []string{"factions", "list", "--primary"}, []string{"leadership"}, []string{"encounter"}},
		{"cards list", []string{"cards", "list"}, []string{"01067", "Maria Hill", "First Aid"}, nil},
		{"cards list filtered", []string{"cards", "list", "--type", "event"}, []string{"First Aid"}, []string{"Maria Hill"}},
		{"cards list with encounter", []string{"cards", "list", "--encounter"}, []string{"Rhino"}, nil},
		{"cards list by pack", []string{"cards", "list", "--pack", "core"}, []string{"Maria Hill"}, nil},
		{"cards search", []string{"cards", "search", "heal"}, []string{"First Aid"}, []string{"Maria Hill"}},
		{"cards search by trait", []string{"cards", "list", "--trait", "shield"}, []string{"Maria Hill"}, []string{"First Aid"}},
		{"cards get", []string{"cards", "get", "01067"}, []string{"Maria Hill"}, nil},
		{"cards get as text", []string{"cards", "get", "01067", "-o", "text"}, []string{"Maria Hill", "Response: draw a card."}, []string{"<b>"}},
		{"cards image", []string{"cards", "image", "01067"}, []string{"/bundles/cards/01067.png"}, nil},
		{"faq", []string{"faq", "01067", "-o", "text"}, []string{"Q: Really?"}, []string{"<strong>"}},
		{"decklists get", []string{"decklists", "get", "35109"}, []string{"Wolverine aggro", "aggression"}, nil},
		{"decklists get as text", []string{"decklists", "get", "35109", "-o", "text", "--with-cards"}, []string{"Wolverine aggro", "Maria Hill"}, nil},
		{"decklists by date", []string{"decklists", "by-date", "2024-01-15"}, []string{"Wolverine aggro"}, nil},
		{"decklists popular", []string{"decklists", "popular"}, []string{"Popular deck"}, nil},
		{"decks get", []string{"decks", "get", "35109"}, []string{"Shared deck"}, nil},
		{"ids format", []string{"cards", "list", "-o", "ids"}, []string{"01067\n"}, []string{"Maria Hill"}},
		{"csv format", []string{"cards", "list", "-o", "csv"}, []string{"CODE,NAME"}, nil},
		{"no headers", []string{"cards", "list", "--no-headers"}, []string{"Maria Hill"}, []string{"CODE"}},
		{"limit", []string{"cards", "list", "--limit", "1"}, []string{"01067"}, []string{"01086"}},
		{"version", []string{"version"}, []string{"marvelcdb"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeMarvelCDB(t)
			got, err := runCaptured(t, srv, tt.args...)
			if err != nil {
				t.Fatalf("run(%v) = %v\noutput:\n%s", tt.args, err, got)
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q:\n%s", want, got)
				}
			}
			for _, unwanted := range tt.absent {
				if strings.Contains(got, unwanted) {
					t.Errorf("output should not contain %q:\n%s", unwanted, got)
				}
			}
		})
	}
}

func TestCardsGetJSONIsValid(t *testing.T) {
	srv := newFakeMarvelCDB(t)
	got, err := runCaptured(t, srv, "cards", "get", "01067", "-o", "json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var card map[string]any
	if err := json.Unmarshal([]byte(got), &card); err != nil {
		t.Fatalf("output was not valid JSON: %v\n%s", err, got)
	}
	if card["code"] != "01067" {
		t.Errorf("code = %v", card["code"])
	}
}

func TestUnknownCardExitsAsNotFound(t *testing.T) {
	srv := newFakeMarvelCDB(t)
	_, _, err := run(t, srv, "cards", "get", "99999")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := exitCode(err); got != 4 {
		t.Errorf("exit code = %d, want 4 (not found)", got)
	}
}

func TestSignedOutDeckCommandsExplainThemselves(t *testing.T) {
	srv := newFakeMarvelCDB(t)
	_, _, err := run(t, srv, "mydecks", "list")
	if err == nil {
		t.Fatal("expected an error when signed out")
	}
	if !strings.Contains(err.Error(), "auth client set") {
		t.Errorf("error should say how to fix it, got: %v", err)
	}
	if got := exitCode(err); got != 3 {
		t.Errorf("exit code = %d, want 3 (unauthorized)", got)
	}
}

// The second identical query must not touch the network.
func TestCachingAvoidsRepeatRequests(t *testing.T) {
	srv := newFakeMarvelCDB(t)
	dir := t.TempDir()
	t.Setenv("MARVELCDB_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("MARVELCDB_CACHE_DIR", filepath.Join(dir, "cache"))

	exec := func(args ...string) {
		t.Helper()
		root := NewRootCommand()
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(append([]string{"--base-url", srv.URL, "--rate-limit", "0", "-q"}, args...))
		stdout, restore := captureStdout(t)
		err := root.ExecuteContext(context.Background())
		restore(stdout)
		if err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
	}

	exec("packs", "list")
	after := srv.requests.Load()
	exec("packs", "list")
	if srv.requests.Load() != after {
		t.Errorf("second run made %d extra request(s); it should have been served from cache",
			srv.requests.Load()-after)
	}

	exec("packs", "list", "--no-cache")
	if srv.requests.Load() != after+1 {
		t.Errorf("--no-cache should have gone to the network")
	}
}

func TestOfflineWithoutACacheFails(t *testing.T) {
	srv := newFakeMarvelCDB(t)
	_, _, err := run(t, srv, "packs", "list", "--offline")
	if err == nil {
		t.Fatal("expected an offline error")
	}
	if got := exitCode(err); got != 5 {
		t.Errorf("exit code = %d, want 5 (offline)", got)
	}
}

func TestConfigCommands(t *testing.T) {
	srv := newFakeMarvelCDB(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	t.Setenv("MARVELCDB_CONFIG", path)
	t.Setenv("MARVELCDB_CACHE_DIR", filepath.Join(dir, "cache"))

	exec := func(args ...string) string {
		t.Helper()
		root := NewRootCommand()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(io.Discard)
		root.SetArgs(append([]string{"--base-url", srv.URL}, args...))
		stdout, restore := captureStdout(t)
		err := root.ExecuteContext(context.Background())
		captured := restore(stdout)
		if err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
		return out.String() + captured
	}

	if got := strings.TrimSpace(exec("config", "path")); got != path {
		t.Errorf("config path = %q, want %q", got, path)
	}
	exec("config", "set", "output", "json")
	if got := strings.TrimSpace(exec("config", "get", "output")); got != "json" {
		t.Errorf("config get output = %q, want json", got)
	}
	if !strings.Contains(exec("config", "list"), "cache_ttl") {
		t.Error("config list should show every setting")
	}
}

func TestInvalidFlagsAreRejected(t *testing.T) {
	srv := newFakeMarvelCDB(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"bad output format", []string{"packs", "list", "-o", "xml"}, "unknown output format"},
		{"bad sort field", []string{"cards", "list", "--sort", "vibes"}, "unknown sort field"},
		{"bad date", []string{"decklists", "by-date", "15-01-2024"}, "YYYY-MM-DD"},
		{"bad colour", []string{"packs", "list", "--color", "rainbow"}, "must be auto"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := run(t, srv, tt.args...)
			if err == nil {
				t.Fatalf("expected an error for %v", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestCommandTreeIsWellFormed(t *testing.T) {
	t.Parallel()

	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Short == "" {
			t.Errorf("%s has no short description", c.CommandPath())
		}
		if !c.HasSubCommands() && c.RunE == nil && c.Run == nil {
			t.Errorf("%s is a leaf with nothing to run", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(NewRootCommand())
}
