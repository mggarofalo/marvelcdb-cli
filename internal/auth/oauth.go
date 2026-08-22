package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// MarvelCDB's OAuth2 endpoints, relative to the site origin.
const (
	AuthPath  = "/oauth/v2/auth"
	TokenPath = "/oauth/v2/token" //nolint:gosec // G101: a URL path, not a credential
)

// DefaultRedirectURI is the loopback address `auth login` listens on. MarvelCDB
// matches redirect URIs exactly against what was registered for the client, so
// this must be the value you gave when requesting credentials.
const DefaultRedirectURI = "http://127.0.0.1:8765/callback"

// Endpoint builds the OAuth2 endpoint pair for a MarvelCDB origin.
func Endpoint(baseURL string) (oauth2.Endpoint, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return oauth2.Endpoint{}, fmt.Errorf("parse base url %q: %w", baseURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return oauth2.Endpoint{}, fmt.Errorf("base url %q must be absolute", baseURL)
	}
	trimmed := strings.TrimSuffix(u.String(), "/")
	return oauth2.Endpoint{
		AuthURL:   trimmed + AuthPath,
		TokenURL:  trimmed + TokenPath,
		AuthStyle: oauth2.AuthStyleInParams,
	}, nil
}

// Config assembles the oauth2.Config for a client against a MarvelCDB origin.
func Config(baseURL string, c ClientCredentials) (*oauth2.Config, error) {
	ep, err := Endpoint(baseURL)
	if err != nil {
		return nil, err
	}
	redirect := c.RedirectURI
	if redirect == "" {
		redirect = DefaultRedirectURI
	}
	return &oauth2.Config{
		ClientID:     c.ID,
		ClientSecret: c.Secret,
		Endpoint:     ep,
		RedirectURL:  redirect,
	}, nil
}

// LoginOptions tunes the interactive authorization-code flow.
type LoginOptions struct {
	// Manual skips the local callback server: the URL is printed, the user
	// authorises in a browser, and pastes the resulting code back. Use it when
	// the client is registered with a non-loopback redirect URI.
	Manual bool
	// NoBrowser prints the URL rather than opening it.
	NoBrowser bool
	// Timeout bounds how long to wait for the callback.
	Timeout time.Duration
	// Prompt reads the pasted code in manual mode.
	Prompt func(url string) (string, error)
	// Notify reports progress to the user.
	Notify func(format string, args ...any)
	// OpenBrowser is swappable for tests.
	OpenBrowser func(url string) error
}

// Login runs the authorization-code flow and returns the resulting token.
func Login(ctx context.Context, cfg *oauth2.Config, opts LoginOptions) (*oauth2.Token, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("client id and secret are required; run `marvelcdb auth client set` first")
	}
	notify := opts.Notify
	if notify == nil {
		notify = func(string, ...any) {}
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	state, err := randomState()
	if err != nil {
		return nil, err
	}
	authURL := cfg.AuthCodeURL(state)

	if opts.Manual {
		if opts.Prompt == nil {
			return nil, errors.New("manual login needs a prompt function")
		}
		code, err := opts.Prompt(authURL)
		if err != nil {
			return nil, err
		}
		code = extractCode(code)
		if code == "" {
			return nil, errors.New("no authorization code provided")
		}
		return exchange(ctx, cfg, code)
	}

	listener, err := listenFor(cfg.RedirectURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = listener.Close() }()

	callbackPath := "/"
	if u, err := url.Parse(cfg.RedirectURL); err == nil && u.Path != "" {
		callbackPath = u.Path
	}

	type result struct {
		code string
		err  error
	}
	results := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errCode := q.Get("error"); errCode != "" {
			msg := strings.TrimSpace(errCode + ": " + q.Get("error_description"))
			writePage(w, http.StatusBadRequest, "Authorization failed", msg)
			results <- result{err: fmt.Errorf("authorization denied: %s", msg)}
			return
		}
		if got := q.Get("state"); got != state {
			writePage(w, http.StatusBadRequest, "Authorization failed", "State mismatch; the request may have been tampered with.")
			results <- result{err: errors.New("state mismatch in OAuth2 callback")}
			return
		}
		code := q.Get("code")
		if code == "" {
			writePage(w, http.StatusBadRequest, "Authorization failed", "No authorization code in the callback.")
			results <- result{err: errors.New("no authorization code in callback")}
			return
		}
		writePage(w, http.StatusOK, "Signed in", "You can close this tab and return to your terminal.")
		results <- result{code: code}
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if opts.NoBrowser {
		notify("Open this URL to authorise marvelcdb-cli:\n\n  %s\n", authURL)
	} else {
		open := opts.OpenBrowser
		if open == nil {
			open = OpenBrowser
		}
		notify("Opening your browser to authorise marvelcdb-cli.")
		notify("If it does not open, visit:\n\n  %s\n", authURL)
		if err := open(authURL); err != nil {
			notify("Could not open a browser (%v); use the URL above.", err)
		}
	}
	notify("Waiting for the callback on %s ...", cfg.RedirectURL)

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-waitCtx.Done():
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("timed out after %s waiting for the OAuth2 callback", timeout)
		}
		return nil, waitCtx.Err()
	case res := <-results:
		if res.err != nil {
			return nil, res.err
		}
		return exchange(ctx, cfg, res.code)
	}
}

func exchange(ctx context.Context, cfg *oauth2.Config, code string) (*oauth2.Token, error) {
	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", err)
	}
	return tok, nil
}

// listenFor binds the loopback port named by a redirect URI.
func listenFor(redirectURI string) (net.Listener, error) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return nil, fmt.Errorf("parse redirect uri %q: %w", redirectURI, err)
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1" {
		return nil, fmt.Errorf("redirect uri %q is not a loopback address; re-run with --manual", redirectURI)
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	addr := net.JoinHostPort(host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s for the OAuth2 callback: %w", addr, err)
	}
	return ln, nil
}

// extractCode accepts either a bare code or the whole redirected URL, which is
// what people usually have in their clipboard.
func extractCode(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	if u, err := url.Parse(input); err == nil && u.Query().Get("code") != "" {
		return u.Query().Get("code")
	}
	if strings.HasPrefix(input, "code=") {
		return strings.TrimPrefix(input, "code=")
	}
	return input
}

func randomState() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate OAuth2 state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func writePage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title></head>`+
		`<body style="font-family:system-ui,sans-serif;margin:4rem auto;max-width:32rem;text-align:center">`+
		`<h1>%s</h1><p>%s</p></body></html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(message))
}

// OpenBrowser launches the platform's default browser.
func OpenBrowser(target string) error {
	if _, err := url.Parse(target); err != nil {
		return fmt.Errorf("refusing to open %q: %w", target, err)
	}
	var cmd string
	args := make([]string, 0, 2)
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	args = append(args, target)
	// The program name is one of three fixed values; only the URL varies, and it
	// is passed as an argument rather than through a shell.
	return exec.Command(cmd, args...).Start() //nolint:gosec // G204: fixed command, argument is not shell-interpreted
}

// PersistingTokenSource wraps ts and writes refreshed tokens back to the store,
// so a long-lived refresh token survives across invocations.
func PersistingTokenSource(store *Store, ts oauth2.TokenSource, seed *oauth2.Token) oauth2.TokenSource {
	last := ""
	if seed != nil {
		last = seed.AccessToken
	}
	return &persisting{store: store, base: ts, last: last}
}

type persisting struct {
	store *Store
	base  oauth2.TokenSource
	last  string
}

func (p *persisting) Token() (*oauth2.Token, error) {
	tok, err := p.base.Token()
	if err != nil {
		return nil, err
	}
	if tok.AccessToken != p.last {
		p.last = tok.AccessToken
		// A failed write must not break the command in progress; the token is
		// still usable for this run.
		_ = p.store.SaveToken(tok)
	}
	return tok, nil
}
