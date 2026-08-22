// Package auth stores MarvelCDB credentials in the operating system keyring and
// drives the OAuth2 authorization-code flow.
//
// Nothing secret is ever written to the config file: the OS keyring (Keychain
// on macOS, Secret Service on Linux, Credential Manager on Windows) holds the
// client secret and the access and refresh tokens.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

// DefaultService is the keyring service name entries are filed under.
const DefaultService = "marvelcdb-cli"

// Keyring accounts within the service.
const (
	clientAccount = "oauth-client"
	tokenAccount  = "oauth-token" //nolint:gosec // G101: a keyring account name, not a credential
)

// Environment overrides, for CI and headless machines with no keyring.
const (
	EnvClientID     = "MARVELCDB_CLIENT_ID"
	EnvClientSecret = "MARVELCDB_CLIENT_SECRET"
	EnvAccessToken  = "MARVELCDB_ACCESS_TOKEN"
	EnvRefreshToken = "MARVELCDB_REFRESH_TOKEN" //nolint:gosec // G101: a variable name, not a credential
)

// ErrNoCredentials reports that nothing is stored and no environment override
// is set.
var ErrNoCredentials = errors.New("no credentials stored")

// Backend is the subset of the keyring the store needs. It exists so tests can
// run without touching a real keyring.
type Backend interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}

type systemKeyring struct{}

func (systemKeyring) Get(service, account string) (string, error) {
	v, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNoCredentials
	}
	return v, err
}

func (systemKeyring) Set(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

func (systemKeyring) Delete(service, account string) error {
	err := keyring.Delete(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNoCredentials
	}
	return err
}

// MemoryBackend is an in-process Backend for tests.
type MemoryBackend struct{ items map[string]string }

// NewMemoryBackend returns an empty in-process keyring.
func NewMemoryBackend() *MemoryBackend { return &MemoryBackend{items: map[string]string{}} }

// Get implements Backend.
func (m *MemoryBackend) Get(service, account string) (string, error) {
	v, ok := m.items[service+"/"+account]
	if !ok {
		return "", ErrNoCredentials
	}
	return v, nil
}

// Set implements Backend.
func (m *MemoryBackend) Set(service, account, secret string) error {
	m.items[service+"/"+account] = secret
	return nil
}

// Delete implements Backend.
func (m *MemoryBackend) Delete(service, account string) error {
	key := service + "/" + account
	if _, ok := m.items[key]; !ok {
		return ErrNoCredentials
	}
	delete(m.items, key)
	return nil
}

// ClientCredentials is the OAuth2 client MarvelCDB issues to a registered
// application. MarvelCDB hands these out by email; see `marvelcdb auth client`.
type ClientCredentials struct {
	ID     string `json:"client_id"`
	Secret string `json:"client_secret"`
	// RedirectURI must match what was registered with MarvelCDB exactly.
	RedirectURI string `json:"redirect_uri,omitempty"`
}

// Store reads and writes credentials.
type Store struct {
	service string
	backend Backend
	// LookupEnv is swappable for tests; it defaults to os.LookupEnv.
	LookupEnv func(string) (string, bool)
}

// NewStore returns a Store backed by the system keyring.
func NewStore(service string) *Store {
	if service == "" {
		service = DefaultService
	}
	return &Store{service: service, backend: systemKeyring{}, LookupEnv: os.LookupEnv}
}

// NewStoreWithBackend returns a Store backed by b.
func NewStoreWithBackend(service string, b Backend) *Store {
	if service == "" {
		service = DefaultService
	}
	return &Store{service: service, backend: b, LookupEnv: func(string) (string, bool) { return "", false }}
}

// Service reports the keyring service name in use.
func (s *Store) Service() string { return s.service }

func (s *Store) env(key string) (string, bool) {
	if s.LookupEnv == nil {
		return "", false
	}
	v, ok := s.LookupEnv(key)
	return strings.TrimSpace(v), ok && strings.TrimSpace(v) != ""
}

// SaveClient stores the OAuth2 client credentials.
func (s *Store) SaveClient(c ClientCredentials) error {
	if c.ID == "" || c.Secret == "" {
		return errors.New("client id and secret are both required")
	}
	// Serialising the secret is the point: the result goes straight into the
	// OS keyring and never touches the filesystem.
	blob, err := json.Marshal(c) //nolint:gosec // G117: destined for the keyring
	if err != nil {
		return fmt.Errorf("encode client credentials: %w", err)
	}
	if err := s.backend.Set(s.service, clientAccount, string(blob)); err != nil {
		return fmt.Errorf("write client credentials to keyring: %w", err)
	}
	return nil
}

// LoadClient reads the OAuth2 client credentials, preferring the environment.
func (s *Store) LoadClient() (ClientCredentials, error) {
	if id, ok := s.env(EnvClientID); ok {
		secret, _ := s.env(EnvClientSecret)
		return ClientCredentials{ID: id, Secret: secret}, nil
	}
	blob, err := s.backend.Get(s.service, clientAccount)
	if err != nil {
		if errors.Is(err, ErrNoCredentials) {
			return ClientCredentials{}, ErrNoCredentials
		}
		return ClientCredentials{}, fmt.Errorf("read client credentials from keyring: %w", err)
	}
	var c ClientCredentials
	if err := json.Unmarshal([]byte(blob), &c); err != nil {
		return ClientCredentials{}, fmt.Errorf("decode stored client credentials: %w", err)
	}
	return c, nil
}

// DeleteClient forgets the OAuth2 client credentials.
func (s *Store) DeleteClient() error {
	if err := s.backend.Delete(s.service, clientAccount); err != nil && !errors.Is(err, ErrNoCredentials) {
		return fmt.Errorf("delete client credentials: %w", err)
	}
	return nil
}

// SaveToken stores an access token.
func (s *Store) SaveToken(tok *oauth2.Token) error {
	if tok == nil || tok.AccessToken == "" {
		return errors.New("refusing to store an empty token")
	}
	blob, err := json.Marshal(tok) //nolint:gosec // G117: destined for the keyring
	if err != nil {
		return fmt.Errorf("encode token: %w", err)
	}
	if err := s.backend.Set(s.service, tokenAccount, string(blob)); err != nil {
		return fmt.Errorf("write token to keyring: %w", err)
	}
	return nil
}

// LoadToken reads the stored access token, preferring the environment.
func (s *Store) LoadToken() (*oauth2.Token, error) {
	if access, ok := s.env(EnvAccessToken); ok {
		refresh, _ := s.env(EnvRefreshToken)
		return &oauth2.Token{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer"}, nil
	}
	blob, err := s.backend.Get(s.service, tokenAccount)
	if err != nil {
		if errors.Is(err, ErrNoCredentials) {
			return nil, ErrNoCredentials
		}
		return nil, fmt.Errorf("read token from keyring: %w", err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(blob), &tok); err != nil {
		return nil, fmt.Errorf("decode stored token: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, ErrNoCredentials
	}
	return &tok, nil
}

// DeleteToken forgets the stored access token.
func (s *Store) DeleteToken() error {
	if err := s.backend.Delete(s.service, tokenAccount); err != nil && !errors.Is(err, ErrNoCredentials) {
		return fmt.Errorf("delete token: %w", err)
	}
	return nil
}

// Status summarises what is stored, without revealing any secret.
type Status struct {
	HasClient    bool      `json:"has_client"`
	ClientID     string    `json:"client_id,omitempty"`
	ClientSource string    `json:"client_source,omitempty"`
	HasToken     bool      `json:"has_token"`
	TokenSource  string    `json:"token_source,omitempty"`
	Expiry       time.Time `json:"expiry,omitzero"`
	Expired      bool      `json:"expired"`
	HasRefresh   bool      `json:"has_refresh_token"`
	Service      string    `json:"keyring_service"`
}

// Status reports what credentials are available.
func (s *Store) Status() Status {
	st := Status{Service: s.service}
	if c, err := s.LoadClient(); err == nil && c.ID != "" {
		st.HasClient = true
		st.ClientID = c.ID
		st.ClientSource = "keyring"
		if _, ok := s.env(EnvClientID); ok {
			st.ClientSource = "environment"
		}
	}
	if tok, err := s.LoadToken(); err == nil {
		st.HasToken = true
		st.TokenSource = "keyring"
		if _, ok := s.env(EnvAccessToken); ok {
			st.TokenSource = "environment"
		}
		st.Expiry = tok.Expiry
		st.Expired = !tok.Expiry.IsZero() && !tok.Valid()
		st.HasRefresh = tok.RefreshToken != ""
	}
	return st
}
