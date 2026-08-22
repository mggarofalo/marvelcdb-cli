package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func newTestStore() *Store {
	return NewStoreWithBackend("marvelcdb-cli-test", NewMemoryBackend())
}

func TestClientCredentialsRoundTrip(t *testing.T) {
	t.Parallel()

	s := newTestStore()
	if _, err := s.LoadClient(); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("LoadClient on an empty store = %v, want ErrNoCredentials", err)
	}

	want := ClientCredentials{ID: "1_abc", Secret: "s3cret", RedirectURI: "http://127.0.0.1:8765/callback"}
	if err := s.SaveClient(want); err != nil {
		t.Fatalf("SaveClient: %v", err)
	}
	got, err := s.LoadClient()
	if err != nil {
		t.Fatalf("LoadClient: %v", err)
	}
	if got != want {
		t.Errorf("LoadClient = %+v, want %+v", got, want)
	}

	if err := s.DeleteClient(); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}
	if _, err := s.LoadClient(); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("after delete LoadClient = %v, want ErrNoCredentials", err)
	}
	// Deleting again is not an error; the end state is what matters.
	if err := s.DeleteClient(); err != nil {
		t.Errorf("second DeleteClient = %v, want nil", err)
	}
}

func TestSaveClientRejectsIncompleteCredentials(t *testing.T) {
	t.Parallel()

	s := newTestStore()
	for _, c := range []ClientCredentials{{}, {ID: "1_abc"}, {Secret: "s3cret"}} {
		if err := s.SaveClient(c); err == nil {
			t.Errorf("SaveClient(%+v) = nil, want an error", c)
		}
	}
}

func TestTokenRoundTrip(t *testing.T) {
	t.Parallel()

	s := newTestStore()
	if _, err := s.LoadToken(); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("LoadToken on an empty store = %v, want ErrNoCredentials", err)
	}

	want := &oauth2.Token{
		AccessToken:  "access",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour).Truncate(time.Second),
	}
	if err := s.SaveToken(want); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	got, err := s.LoadToken()
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Errorf("LoadToken = %+v, want %+v", got, want)
	}
	if !got.Expiry.Equal(want.Expiry) {
		t.Errorf("expiry = %v, want %v", got.Expiry, want.Expiry)
	}

	if err := s.SaveToken(nil); err == nil {
		t.Error("SaveToken(nil) = nil, want an error")
	}
	if err := s.SaveToken(&oauth2.Token{}); err == nil {
		t.Error("SaveToken with an empty access token = nil, want an error")
	}
}

func TestEnvironmentOverridesTheKeyring(t *testing.T) {
	t.Parallel()

	s := NewStoreWithBackend("marvelcdb-cli-test", NewMemoryBackend())
	if err := s.SaveClient(ClientCredentials{ID: "keyring-id", Secret: "keyring-secret"}); err != nil {
		t.Fatalf("SaveClient: %v", err)
	}
	if err := s.SaveToken(&oauth2.Token{AccessToken: "keyring-token"}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	env := map[string]string{
		EnvClientID:     "env-id",
		EnvClientSecret: "env-secret",
		EnvAccessToken:  "env-token",
		EnvRefreshToken: "env-refresh",
	}
	s.LookupEnv = func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}

	creds, err := s.LoadClient()
	if err != nil {
		t.Fatalf("LoadClient: %v", err)
	}
	if creds.ID != "env-id" || creds.Secret != "env-secret" {
		t.Errorf("LoadClient = %+v, want the environment values", creds)
	}

	tok, err := s.LoadToken()
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if tok.AccessToken != "env-token" || tok.RefreshToken != "env-refresh" {
		t.Errorf("LoadToken = %+v, want the environment values", tok)
	}

	status := s.Status()
	if status.ClientSource != "environment" || status.TokenSource != "environment" {
		t.Errorf("status sources = %q/%q, want environment", status.ClientSource, status.TokenSource)
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()

	s := newTestStore()
	empty := s.Status()
	if empty.HasClient || empty.HasToken {
		t.Errorf("empty store status = %+v, want nothing stored", empty)
	}

	if err := s.SaveClient(ClientCredentials{ID: "1_abc", Secret: "s3cret"}); err != nil {
		t.Fatalf("SaveClient: %v", err)
	}
	if err := s.SaveToken(&oauth2.Token{
		AccessToken:  "access",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	got := s.Status()
	if !got.HasClient || got.ClientID != "1_abc" {
		t.Errorf("status client = %+v", got)
	}
	if !got.HasToken || !got.HasRefresh {
		t.Errorf("status token = %+v", got)
	}
	if !got.Expired {
		t.Error("an expiry in the past should read as expired")
	}
	if got.Service != "marvelcdb-cli-test" {
		t.Errorf("service = %q", got.Service)
	}
}

// The status struct is printed to the terminal, so it must never carry the
// secret or the token itself.
func TestStatusNeverCarriesSecrets(t *testing.T) {
	t.Parallel()

	s := newTestStore()
	if err := s.SaveClient(ClientCredentials{ID: "1_abc", Secret: "super-secret-value"}); err != nil {
		t.Fatalf("SaveClient: %v", err)
	}
	if err := s.SaveToken(&oauth2.Token{AccessToken: "super-secret-token"}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	rendered := strings.ToLower(strings.Join([]string{
		s.Status().ClientID, s.Status().ClientSource, s.Status().TokenSource, s.Status().Service,
	}, " "))
	for _, secret := range []string{"super-secret-value", "super-secret-token"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("status leaked %q", secret)
		}
	}
}

func TestMemoryBackendDelete(t *testing.T) {
	t.Parallel()

	b := NewMemoryBackend()
	if err := b.Delete("svc", "acct"); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("deleting a missing entry = %v, want ErrNoCredentials", err)
	}
	if err := b.Set("svc", "acct", "value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, err := b.Get("svc", "acct"); err != nil || v != "value" {
		t.Errorf("Get = %q, %v", v, err)
	}
	if err := b.Delete("svc", "acct"); err != nil {
		t.Errorf("Delete = %v", err)
	}
}
