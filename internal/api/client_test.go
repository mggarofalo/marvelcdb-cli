package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func newTestClient(t *testing.T, handler http.Handler, opts ...Option) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	all := append([]Option{
		WithBaseURL(srv.URL),
		WithRateLimit(0),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	}, opts...)
	c, err := New(all...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

func TestPublicEndpointPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		call      func(context.Context, *Client) error
		wantPath  string
		wantQuery string
		body      string
	}{
		{
			name:     "packs",
			call:     func(ctx context.Context, c *Client) error { _, err := c.Packs(ctx); return err },
			wantPath: "/api/public/packs/",
			body:     `[{"code":"core","name":"Core Set"}]`,
		},
		{
			name:     "factions",
			call:     func(ctx context.Context, c *Client) error { _, err := c.Factions(ctx); return err },
			wantPath: "/api/public/factions/",
			body:     `[{"code":"justice","name":"Justice","is_primary":true}]`,
		},
		{
			name:     "cards without encounter",
			call:     func(ctx context.Context, c *Client) error { _, err := c.Cards(ctx, false); return err },
			wantPath: "/api/public/cards/",
			body:     `[]`,
		},
		{
			name:      "cards with encounter",
			call:      func(ctx context.Context, c *Client) error { _, err := c.Cards(ctx, true); return err },
			wantPath:  "/api/public/cards/",
			wantQuery: "encounter=1",
			body:      `[]`,
		},
		{
			name:     "cards by pack",
			call:     func(ctx context.Context, c *Client) error { _, err := c.CardsByPack(ctx, "core"); return err },
			wantPath: "/api/public/cards/core.json",
			body:     `[]`,
		},
		{
			name:     "single card",
			call:     func(ctx context.Context, c *Client) error { _, err := c.Card(ctx, "01001a"); return err },
			wantPath: "/api/public/card/01001a.json",
			body:     `{"code":"01001a"}`,
		},
		{
			name:     "faq",
			call:     func(ctx context.Context, c *Client) error { _, err := c.Faq(ctx, "01001a"); return err },
			wantPath: "/api/public/faq/01001a.json",
			body:     `[{"code":"01001a","html":"<p>Q</p>"}]`,
		},
		{
			name:     "taboos",
			call:     func(ctx context.Context, c *Client) error { _, err := c.Taboos(ctx); return err },
			wantPath: "/api/public/taboos/",
			body:     `[]`,
		},
		{
			name:     "decklist",
			call:     func(ctx context.Context, c *Client) error { _, err := c.Decklist(ctx, 35109); return err },
			wantPath: "/api/public/decklist/35109.json",
			body:     `{"id":35109}`,
		},
		{
			name:     "decklists by date",
			call:     func(ctx context.Context, c *Client) error { _, err := c.DecklistsByDate(ctx, "2024-01-15"); return err },
			wantPath: "/api/public/decklists/by_date/2024-01-15.json",
			body:     `[]`,
		},
		{
			name:     "popular decklists",
			call:     func(ctx context.Context, c *Client) error { _, err := c.PopularDecklists(ctx); return err },
			wantPath: "/api/public/decklists/popular/",
			body:     `[]`,
		},
		{
			name:     "public deck",
			call:     func(ctx context.Context, c *Client) error { _, err := c.PublicDeck(ctx, 35109); return err },
			wantPath: "/api/public/deck/35109.json",
			body:     `{"id":35109}`,
		},
		{
			name:     "my decks",
			call:     func(ctx context.Context, c *Client) error { _, err := c.MyDecks(ctx); return err },
			wantPath: "/api/oauth2/decks",
			body:     `[]`,
		},
		{
			name:     "load deck",
			call:     func(ctx context.Context, c *Client) error { _, err := c.LoadDeck(ctx, 7); return err },
			wantPath: "/api/oauth2/deck/load/7",
			body:     `{"id":7}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotPath, gotQuery, gotAccept string
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotQuery, gotAccept = r.URL.Path, r.URL.RawQuery, r.Header.Get("Accept")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))

			if err := tt.call(context.Background(), c); err != nil {
				t.Fatalf("call: %v", err)
			}
			if gotPath != tt.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if gotQuery != tt.wantQuery {
				t.Errorf("query = %q, want %q", gotQuery, tt.wantQuery)
			}
			if gotAccept != "application/json" {
				t.Errorf("Accept = %q, want application/json", gotAccept)
			}
		})
	}
}

func TestNotFoundClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		body       string
		call       func(context.Context, *Client) error
		wantIsMiss bool
	}{
		{
			name:   "single record 500 envelope means missing",
			status: http.StatusInternalServerError,
			body:   `{"error":{"code":500,"message":"Internal Server Error"}}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.Card(ctx, "99999")
				return err
			},
			wantIsMiss: true,
		},
		{
			name:   "collection 500 stays a server error",
			status: http.StatusInternalServerError,
			body:   `{"error":{"code":500,"message":"Internal Server Error"}}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.Taboos(ctx)
				return err
			},
			wantIsMiss: false,
		},
		{
			name:   "404 is missing",
			status: http.StatusNotFound,
			body:   `{"error":{"code":404,"message":"Not Found"}}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.Decklist(ctx, 1)
				return err
			},
			wantIsMiss: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))

			err := tt.call(context.Background(), c)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := IsNotFound(err); got != tt.wantIsMiss {
				t.Errorf("IsNotFound = %v, want %v (err: %v)", got, tt.wantIsMiss, err)
			}
		})
	}
}

func TestUnauthorizedClassification(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"token expired"}`)
	}))

	_, err := c.MyDecks(context.Background())
	if !IsUnauthorized(err) {
		t.Fatalf("IsUnauthorized = false, want true (err: %v)", err)
	}
	if !strings.Contains(err.Error(), "token expired") {
		t.Errorf("error should surface the description, got %q", err.Error())
	}
}

func TestInputValidation(t *testing.T) {
	t.Parallel()

	c, err := New(WithBaseURL("https://example.invalid"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	tests := []struct {
		name string
		err  error
	}{
		{"empty card code", mustErr(func() error { _, e := c.Card(ctx, ""); return e })},
		{"card code with a slash", mustErr(func() error { _, e := c.Card(ctx, "../../etc/passwd"); return e })},
		{"bad date", mustErr(func() error { _, e := c.DecklistsByDate(ctx, "15-01-2024"); return e })},
		{"impossible date", mustErr(func() error { _, e := c.DecklistsByDate(ctx, "2024-13-45"); return e })},
		{"zero decklist id", mustErr(func() error { _, e := c.Decklist(ctx, 0); return e })},
		{"negative deck id", mustErr(func() error { _, e := c.PublicDeck(ctx, -1); return e })},
	}
	for _, tt := range tests {
		if tt.err == nil {
			t.Errorf("%s: expected a validation error before any request", tt.name)
		}
	}
}

func mustErr(fn func() error) error { return fn() }

func TestLocaleRewritesHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		locale string
		base   string
		want   string
	}{
		{"", "https://marvelcdb.com", "marvelcdb.com"},
		{"en", "https://marvelcdb.com", "marvelcdb.com"},
		{"de", "https://marvelcdb.com", "de.marvelcdb.com"},
		{"fr", "https://www.marvelcdb.com", "fr.marvelcdb.com"},
		{"it", "https://de.marvelcdb.com", "it.marvelcdb.com"},
		{"de", "http://localhost:8080", "localhost:8080"},
	}
	for _, tt := range tests {
		c, err := New(WithBaseURL(tt.base), WithLocale(tt.locale))
		if err != nil {
			t.Fatalf("New(%q, %q): %v", tt.base, tt.locale, err)
		}
		u, err := url.Parse(c.BaseURL())
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if u.Host != tt.want {
			t.Errorf("locale %q on %q gave host %q, want %q", tt.locale, tt.base, u.Host, tt.want)
		}
	}
}

func TestNewRejectsRelativeBaseURL(t *testing.T) {
	t.Parallel()
	if _, err := New(WithBaseURL("marvelcdb.com")); err == nil {
		t.Fatal("expected an error for a base URL with no scheme")
	}
}

func TestTokenIsOnlySentToProtectedPaths(t *testing.T) {
	t.Parallel()

	var publicAuth, protectedAuth string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/oauth2/") {
			protectedAuth = r.Header.Get("Authorization")
			_, _ = io.WriteString(w, `[]`)
			return
		}
		publicAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `[]`)
	}), WithTokenSource(staticTokenSource("secret-token")))

	ctx := context.Background()
	if _, err := c.Packs(ctx); err != nil {
		t.Fatalf("Packs: %v", err)
	}
	if _, err := c.MyDecks(ctx); err != nil {
		t.Fatalf("MyDecks: %v", err)
	}

	if publicAuth != "" {
		t.Errorf("public request carried Authorization %q; it must stay anonymous and cacheable", publicAuth)
	}
	if protectedAuth != "Bearer secret-token" {
		t.Errorf("protected request Authorization = %q, want Bearer secret-token", protectedAuth)
	}
}

func TestUserAgentIsSent(t *testing.T) {
	t.Parallel()

	var got string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		_, _ = io.WriteString(w, `[]`)
	}), WithUserAgent("marvelcdb-cli/test"))

	if _, err := c.Packs(context.Background()); err != nil {
		t.Fatalf("Packs: %v", err)
	}
	if got != "marvelcdb-cli/test" {
		t.Errorf("User-Agent = %q, want marvelcdb-cli/test", got)
	}
}

func TestContextCancellationIsPropagated(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = io.WriteString(w, `[]`)
	}))
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.Packs(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestSlotsDecoding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"object", `{"slots":{"01050":2,"01051":1}}`, 2},
		{"null", `{"slots":null}`, 0},
		{"empty array", `{"slots":[]}`, 0},
		{"string quantities", `{"slots":{"01050":"3"}}`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var d Deck
			if err := json.Unmarshal([]byte(tt.input), &d); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(d.Slots) != tt.want {
				t.Errorf("len(slots) = %d, want %d", len(d.Slots), tt.want)
			}
		})
	}
}

func TestDeckHelpers(t *testing.T) {
	t.Parallel()

	d := Deck{Meta: `{"aspect":"justice"}`, Slots: Slots{"a": 2, "b": 3}}
	if got := d.Aspect(); got != "justice" {
		t.Errorf("Aspect = %q, want justice", got)
	}
	if got := d.CardCount(); got != 5 {
		t.Errorf("CardCount = %d, want 5", got)
	}

	broken := Deck{Meta: "not json"}
	if got := broken.Aspect(); got != "" {
		t.Errorf("Aspect on invalid meta = %q, want empty", got)
	}
}

func TestCardHelpers(t *testing.T) {
	t.Parallel()

	c := Card{
		Traits:           "Avenger. Aerial.",
		ResourcePhysical: ptr(1),
		ResourceWild:     ptr(2),
	}
	traits := c.TraitList()
	if len(traits) != 2 || traits[0] != "Avenger" || traits[1] != "Aerial" {
		t.Errorf("TraitList = %v, want [Avenger Aerial]", traits)
	}
	if got := c.Resources(); got != 3 {
		t.Errorf("Resources = %d, want 3", got)
	}

	empty := Card{}
	if got := empty.TraitList(); len(got) != 0 {
		t.Errorf("TraitList on a traitless card = %v, want empty", got)
	}
}

func TestPackReleaseDate(t *testing.T) {
	t.Parallel()

	p := Pack{Available: "2019-11-01"}
	got, ok := p.ReleaseDate()
	if !ok || got.Year() != 2019 || got.Month() != time.November {
		t.Errorf("ReleaseDate = %v, %v", got, ok)
	}
	if _, ok := (Pack{}).ReleaseDate(); ok {
		t.Error("an empty Available should not parse")
	}
	if _, ok := (Pack{Available: "soon"}).ReleaseDate(); ok {
		t.Error("a non-date Available should not parse")
	}
}

func TestMutationResultMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want string
	}{
		{`{"success":true,"msg":12345}`, "12345"},
		{`{"success":false,"msg":"Slots missing"}`, "Slots missing"},
		{`{"success":true}`, ""},
	}
	for _, tt := range tests {
		var m MutationResult
		if err := json.Unmarshal([]byte(tt.raw), &m); err != nil {
			t.Fatalf("decode %s: %v", tt.raw, err)
		}
		if got := m.Message(); got != tt.want {
			t.Errorf("Message() for %s = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func staticTokenSource(token string) oauth2.TokenSource {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token, TokenType: "Bearer"})
}

func ptr[T any](v T) *T { return &v }
