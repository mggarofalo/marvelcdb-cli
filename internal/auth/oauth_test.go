package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestEndpointPaths(t *testing.T) {
	t.Parallel()

	ep, err := Endpoint("https://marvelcdb.com/")
	if err != nil {
		t.Fatalf("Endpoint: %v", err)
	}
	if ep.AuthURL != "https://marvelcdb.com/oauth/v2/auth" {
		t.Errorf("AuthURL = %q", ep.AuthURL)
	}
	if ep.TokenURL != "https://marvelcdb.com/oauth/v2/token" {
		t.Errorf("TokenURL = %q", ep.TokenURL)
	}

	if _, err := Endpoint("marvelcdb.com"); err == nil {
		t.Error("a base URL with no scheme should be rejected")
	}
}

func TestConfigDefaultsTheRedirectURI(t *testing.T) {
	t.Parallel()

	cfg, err := Config("https://marvelcdb.com", ClientCredentials{ID: "1_abc", Secret: "s3cret"})
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.RedirectURL != DefaultRedirectURI {
		t.Errorf("RedirectURL = %q, want %q", cfg.RedirectURL, DefaultRedirectURI)
	}

	cfg, err = Config("https://marvelcdb.com", ClientCredentials{
		ID: "1_abc", Secret: "s3cret", RedirectURI: "https://example.com/cb",
	})
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.RedirectURL != "https://example.com/cb" {
		t.Errorf("RedirectURL = %q", cfg.RedirectURL)
	}
}

// The full loopback flow: the CLI opens a URL, MarvelCDB redirects back with a
// code, and the code is exchanged for a token.
func TestLoginLoopbackFlow(t *testing.T) {
	t.Parallel()

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token request: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "authorization_code" {
			t.Errorf("grant_type = %q", got)
		}
		if got := r.Form.Get("code"); got != "the-code" {
			t.Errorf("code = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "at",
			"refresh_token": "rt",
			"token_type":    "bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenSrv.Close()

	cfg := &oauth2.Config{
		ClientID:     "1_abc",
		ClientSecret: "s3cret",
		RedirectURL:  "http://127.0.0.1:8791/callback",
		Endpoint: oauth2.Endpoint{
			AuthURL:   "http://example.invalid/oauth/v2/auth",
			TokenURL:  tokenSrv.URL,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}

	tok, err := Login(context.Background(), cfg, LoginOptions{
		Timeout: 10 * time.Second,
		Notify:  func(string, ...any) {},
		OpenBrowser: func(authURL string) error {
			// Stand in for the browser: follow the redirect ourselves.
			u, err := url.Parse(authURL)
			if err != nil {
				return err
			}
			state := u.Query().Get("state")
			if state == "" {
				t.Error("the authorization URL carried no state")
			}
			go func() {
				callback := cfg.RedirectURL + "?code=the-code&state=" + url.QueryEscape(state)
				resp, err := http.Get(callback) //nolint:noctx // test helper
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" {
		t.Errorf("token = %+v", tok)
	}
}

func TestLoginRejectsAMismatchedState(t *testing.T) {
	t.Parallel()

	cfg := &oauth2.Config{
		ClientID:     "1_abc",
		ClientSecret: "s3cret",
		RedirectURL:  "http://127.0.0.1:8792/callback",
		Endpoint:     oauth2.Endpoint{AuthURL: "http://example.invalid/auth", TokenURL: "http://example.invalid/token"},
	}

	_, err := Login(context.Background(), cfg, LoginOptions{
		Timeout: 5 * time.Second,
		Notify:  func(string, ...any) {},
		OpenBrowser: func(string) error {
			go func() {
				resp, err := http.Get(cfg.RedirectURL + "?code=x&state=wrong") //nolint:noctx // test helper
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}()
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("err = %v, want a state mismatch", err)
	}
}

func TestLoginSurfacesAuthorizationDenial(t *testing.T) {
	t.Parallel()

	cfg := &oauth2.Config{
		ClientID:     "1_abc",
		ClientSecret: "s3cret",
		RedirectURL:  "http://127.0.0.1:8793/callback",
		Endpoint:     oauth2.Endpoint{AuthURL: "http://example.invalid/auth", TokenURL: "http://example.invalid/token"},
	}

	_, err := Login(context.Background(), cfg, LoginOptions{
		Timeout: 5 * time.Second,
		Notify:  func(string, ...any) {},
		OpenBrowser: func(string) error {
			go func() {
				resp, err := http.Get(cfg.RedirectURL + "?error=access_denied&error_description=user+said+no") //nolint:noctx // test helper
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}()
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("err = %v, want the denial reason", err)
	}
}

func TestLoginRequiresCredentials(t *testing.T) {
	t.Parallel()

	_, err := Login(context.Background(), &oauth2.Config{}, LoginOptions{})
	if err == nil || !strings.Contains(err.Error(), "client id and secret") {
		t.Fatalf("err = %v, want a missing-credentials error", err)
	}
}

func TestLoginManualMode(t *testing.T) {
	t.Parallel()

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if got := r.Form.Get("code"); got != "pasted-code" {
			t.Errorf("code = %q, want pasted-code", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"at","token_type":"bearer"}`)
	}))
	defer tokenSrv.Close()

	cfg := &oauth2.Config{
		ClientID:     "1_abc",
		ClientSecret: "s3cret",
		RedirectURL:  "urn:ietf:wg:oauth:2.0:oob",
		Endpoint:     oauth2.Endpoint{AuthURL: "http://example.invalid/auth", TokenURL: tokenSrv.URL},
	}

	tok, err := Login(context.Background(), cfg, LoginOptions{
		Manual: true,
		Prompt: func(string) (string, error) {
			// Users paste the whole redirected URL far more often than the code.
			return "http://127.0.0.1:8765/callback?code=pasted-code&state=abc\n", nil
		},
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok.AccessToken != "at" {
		t.Errorf("access token = %q", tok.AccessToken)
	}
}

func TestExtractCode(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"abc123", "abc123"},
		{"  abc123\n", "abc123"},
		{"code=abc123", "abc123"},
		{"http://127.0.0.1:8765/callback?code=abc123&state=x", "abc123"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := extractCode(tt.in); got != tt.want {
			t.Errorf("extractCode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestListenForRejectsNonLoopback(t *testing.T) {
	t.Parallel()

	if _, err := listenFor("https://example.com/callback"); err == nil {
		t.Fatal("a non-loopback redirect URI should be rejected")
	}
	ln, err := listenFor("http://127.0.0.1:0/callback")
	if err != nil {
		t.Fatalf("listenFor: %v", err)
	}
	_ = ln.Close()
}

// A refreshed token must survive the process, or every command pays for a
// refresh round trip.
func TestPersistingTokenSourceWritesRefreshedTokens(t *testing.T) {
	t.Parallel()

	store := newTestStore()
	refreshed := &oauth2.Token{AccessToken: "new-access", RefreshToken: "rt"}
	src := PersistingTokenSource(store, oauth2.StaticTokenSource(refreshed), &oauth2.Token{AccessToken: "old-access"})

	tok, err := src.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "new-access" {
		t.Errorf("access token = %q", tok.AccessToken)
	}

	stored, err := store.LoadToken()
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if stored.AccessToken != "new-access" {
		t.Errorf("stored token = %q, want the refreshed one", stored.AccessToken)
	}
}

func TestPersistingTokenSourceSkipsUnchangedTokens(t *testing.T) {
	t.Parallel()

	store := newTestStore()
	same := &oauth2.Token{AccessToken: "same"}
	src := PersistingTokenSource(store, oauth2.StaticTokenSource(same), same)

	if _, err := src.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}
	// Nothing changed, so nothing should have been written.
	if _, err := store.LoadToken(); err == nil {
		t.Error("an unchanged token should not be rewritten to the keyring")
	}
}
