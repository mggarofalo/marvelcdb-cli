package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mggarofalo/marvelcdb-cli/internal/auth"
	"github.com/mggarofalo/marvelcdb-cli/internal/output"
)

func newAuthCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage MarvelCDB credentials",
		Long: strings.TrimSpace(`
Manage the credentials used for the OAuth2 endpoints.

MarvelCDB issues OAuth2 client credentials to registered applications only; you
request them by email from the site's maintainers, telling them the redirect URI
your client will use. The default here is http://127.0.0.1:8765/callback, and
MarvelCDB matches redirect URIs exactly, so ask for that one unless you have a
reason not to.

Once you have an id and secret:

  marvelcdb auth client set --id <id> --secret <secret>
  marvelcdb auth login

Secrets are written to the operating system keyring — Keychain on macOS, Secret
Service on Linux, Credential Manager on Windows — never to the config file. On a
machine with no keyring, set MARVELCDB_CLIENT_ID, MARVELCDB_CLIENT_SECRET and
MARVELCDB_ACCESS_TOKEN in the environment instead.`),
	}
	cmd.AddCommand(
		newAuthLoginCommand(f),
		newAuthLogoutCommand(f),
		newAuthStatusCommand(f),
		newAuthClientCommand(f),
		newAuthTokenCommand(f),
	)
	return cmd
}

func newAuthLoginCommand(f *Factory) *cobra.Command {
	var manual, noBrowser bool
	var redirectURI string
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to MarvelCDB",
		Long: strings.TrimSpace(`
Run the OAuth2 authorization-code flow.

By default the CLI starts a one-shot server on the loopback address in your
redirect URI, opens your browser, and captures the callback. If your client is
registered with a redirect URI that is not loopback, use --manual and paste the
resulting URL back into the terminal.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			creds, err := f.store.LoadClient()
			if err != nil {
				if errors.Is(err, auth.ErrNoCredentials) {
					return errNotLoggedIn("no OAuth2 client is configured")
				}
				return err
			}
			if redirectURI != "" {
				creds.RedirectURI = redirectURI
			} else if creds.RedirectURI == "" {
				creds.RedirectURI = f.cfg.RedirectURI
			}

			cfg, err := auth.Config(f.cfg.BaseURL, creds)
			if err != nil {
				return err
			}

			tok, err := auth.Login(cmd.Context(), cfg, auth.LoginOptions{
				Manual:    manual,
				NoBrowser: noBrowser,
				Timeout:   timeout,
				Notify: func(format string, args ...any) {
					if !f.flags.quiet {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
					}
				},
				Prompt: func(authURL string) (string, error) {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"Open this URL, authorise the application, then paste the URL you land on:\n\n  %s\n\nCode or URL: ", authURL)
					line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
					if err != nil && line == "" {
						return "", fmt.Errorf("read authorization code: %w", err)
					}
					return line, nil
				},
			})
			if err != nil {
				return err
			}
			if err := f.store.SaveToken(tok); err != nil {
				return err
			}
			// Remember the redirect URI that actually worked.
			if creds.RedirectURI != "" {
				_ = f.store.SaveClient(creds)
			}

			status := f.store.Status()
			return f.printer.Print(status, authStatusTable(status))
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&manual, "manual", false, "print the URL and read the code back, instead of listening for a callback")
	fl.BoolVar(&noBrowser, "no-browser", false, "print the URL instead of opening a browser")
	fl.StringVar(&redirectURI, "redirect-uri", "", "OAuth2 redirect URI registered for your client")
	fl.DurationVar(&timeout, "wait", 5*time.Minute, "how long to wait for the callback")
	return cmd
}

func newAuthLogoutCommand(f *Factory) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Forget the stored access token",
		Long:  "Delete the stored token. Pass --all to forget the client credentials too.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := f.store.DeleteToken(); err != nil {
				return err
			}
			removed := []string{"token"}
			if all {
				if err := f.store.DeleteClient(); err != nil {
					return err
				}
				removed = append(removed, "client credentials")
			}
			if !f.flags.quiet {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Removed %s from the %s keyring entry.\n",
					strings.Join(removed, " and "), f.store.Service())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "also forget the OAuth2 client id and secret")
	return cmd
}

func newAuthStatusCommand(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show what credentials are stored",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			status := f.store.Status()
			return f.printer.Print(status, authStatusTable(status))
		},
	}
}

func authStatusTable(s auth.Status) *output.Table {
	expiry := "-"
	if !s.Expiry.IsZero() {
		expiry = s.Expiry.Local().Format(time.RFC3339)
		if s.Expired {
			expiry += " (expired)"
		}
	}
	t := &output.Table{
		Headers: []string{"SETTING", "VALUE"},
		Rows: [][]string{
			{"keyring service", s.Service},
			{"client configured", yesNo(s.HasClient)},
			{"client id", orDefault(s.ClientID, "-")},
			{"client source", orDefault(s.ClientSource, "-")},
			{"signed in", yesNo(s.HasToken)},
			{"token source", orDefault(s.TokenSource, "-")},
			{"token expiry", expiry},
			{"refresh token", yesNo(s.HasRefresh)},
		},
	}
	var b strings.Builder
	for _, row := range t.Rows {
		_, _ = fmt.Fprintf(&b, "%-18s %s\n", row[0]+":", row[1])
	}
	t.Text = b.String()
	return t
}

func newAuthClientCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "client",
		Short: "Manage the OAuth2 client id and secret",
	}
	cmd.AddCommand(newAuthClientSetCommand(f), newAuthClientShowCommand(f), newAuthClientClearCommand(f))
	return cmd
}

func newAuthClientSetCommand(f *Factory) *cobra.Command {
	var id, secret, redirectURI string

	cmd := &cobra.Command{
		Use:   "set",
		Short: "Store the OAuth2 client credentials in the keyring",
		Long: "Store the client id and secret MarvelCDB issued you. Omit --secret to be\n" +
			"prompted for it, which keeps the secret out of your shell history.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if id == "" {
				return errors.New("--id is required")
			}
			if secret == "" {
				var err error
				secret, err = promptSecret(cmd.ErrOrStderr(), "Client secret: ")
				if err != nil {
					return err
				}
			}
			if redirectURI == "" {
				redirectURI = f.cfg.RedirectURI
			}
			if err := f.store.SaveClient(auth.ClientCredentials{
				ID: id, Secret: secret, RedirectURI: redirectURI,
			}); err != nil {
				return err
			}
			if !f.flags.quiet {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"Stored client %s in the %s keyring entry.\nNext: marvelcdb auth login\n",
					id, f.store.Service())
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&id, "id", "", "OAuth2 client id (required)")
	fl.StringVar(&secret, "secret", "", "OAuth2 client secret (prompted for when omitted)")
	fl.StringVar(&redirectURI, "redirect-uri", "", "redirect URI registered for this client")
	return cmd
}

func newAuthClientShowCommand(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the stored client id (never the secret)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			creds, err := f.store.LoadClient()
			if err != nil {
				return err
			}
			redacted := map[string]string{
				"client_id":     creds.ID,
				"client_secret": redact(creds.Secret),
				"redirect_uri":  creds.RedirectURI,
			}
			t := &output.Table{
				Headers: []string{"SETTING", "VALUE"},
				Rows: [][]string{
					{"client_id", creds.ID},
					{"client_secret", redact(creds.Secret)},
					{"redirect_uri", creds.RedirectURI},
				},
			}
			return f.printer.Print(redacted, t)
		},
	}
}

func newAuthClientClearCommand(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Forget the stored client credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := f.store.DeleteClient(); err != nil {
				return err
			}
			if !f.flags.quiet {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Client credentials removed.")
			}
			return nil
		},
	}
}

func newAuthTokenCommand(f *Factory) *cobra.Command {
	var refresh bool
	return &cobra.Command{
		Use:   "token",
		Short: "Print the current access token",
		Long: strings.TrimSpace(`
Print the access token, refreshing it first if it has expired.

This exists for scripting: pipe it into curl or another tool. Treat the output
as a secret — anything holding it can act as you on MarvelCDB.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			src, err := f.TokenSource(cmd.Context())
			if err != nil {
				if errors.Is(err, auth.ErrNoCredentials) {
					return errNotLoggedIn("you are not signed in")
				}
				return err
			}
			tok, err := src.Token()
			if err != nil {
				return fmt.Errorf("refresh access token: %w", err)
			}
			value := tok.AccessToken
			if refresh {
				value = tok.RefreshToken
				if value == "" {
					return errors.New("no refresh token is stored")
				}
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), value)
			return nil
		},
	}
}

func redact(secret string) string {
	if secret == "" {
		return "-"
	}
	if len(secret) <= 4 {
		return strings.Repeat("*", len(secret))
	}
	return strings.Repeat("*", len(secret)-4) + secret[len(secret)-4:]
}

// promptSecret reads a secret without echoing it when stdin is a terminal.
func promptSecret(errOut io.Writer, prompt string) (string, error) {
	_, _ = fmt.Fprint(errOut, prompt)
	fd := int(os.Stdin.Fd()) //nolint:gosec // G115: a file descriptor always fits in an int
	if term.IsTerminal(fd) {
		raw, err := term.ReadPassword(fd)
		_, _ = fmt.Fprintln(errOut)
		if err != nil {
			return "", fmt.Errorf("read secret: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read secret: %w", err)
	}
	return strings.TrimSpace(line), nil
}
