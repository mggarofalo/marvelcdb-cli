// Package cli wires the marvelcdb command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/mggarofalo/marvelcdb-cli/internal/api"
	"github.com/mggarofalo/marvelcdb-cli/internal/auth"
	"github.com/mggarofalo/marvelcdb-cli/internal/cache"
	"github.com/mggarofalo/marvelcdb-cli/internal/config"
	"github.com/mggarofalo/marvelcdb-cli/internal/output"
	"github.com/mggarofalo/marvelcdb-cli/internal/version"
)

// globalFlags are the settings every command shares.
type globalFlags struct {
	configPath string
	baseURL    string
	locale     string
	outputFmt  string
	color      string
	noHeaders  bool
	cacheDir   string
	cacheTTL   time.Duration
	noCache    bool
	refresh    bool
	offline    bool
	timeout    time.Duration
	rateLimit  time.Duration
	userAgent  string
	quiet      bool
}

// Factory builds the collaborators commands need, once, on demand.
type Factory struct {
	flags   globalFlags
	cfg     config.Config
	printer *output.Printer
	cache   *cache.Cache
	store   *auth.Store

	client     *api.Client
	authClient *api.Client
}

// changedFunc reports whether a flag was given explicitly. It matters because
// zero is a meaningful value for the duration flags: --rate-limit 0 means "do
// not throttle", not "fall back to the default".
type changedFunc func(name string) bool

// flagsChangedOn adapts a command's flag set to changedFunc.
func flagsChangedOn(cmd *cobra.Command) changedFunc {
	if cmd == nil {
		return func(string) bool { return false }
	}
	return func(name string) bool {
		fl := cmd.Flags().Lookup(name)
		return fl != nil && fl.Changed
	}
}

// resolve builds everything that does not need the network.
func (f *Factory) resolve(changed changedFunc) error {
	if changed == nil {
		changed = func(string) bool { return false }
	}
	path := f.flags.configPath
	if path == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return err
		}
		path = p
	}
	f.flags.configPath = path

	cfg, err := config.Load(path)
	if err != nil {
		return err
	}

	// Command-line flags beat the file and the environment.
	if f.flags.baseURL != "" {
		cfg.BaseURL = f.flags.baseURL
	}
	if f.flags.locale != "" {
		cfg.Locale = f.flags.locale
	}
	if f.flags.outputFmt != "" {
		cfg.Output = f.flags.outputFmt
	}
	if f.flags.color != "" {
		cfg.Color = f.flags.color
	}
	if f.flags.cacheDir != "" {
		cfg.CacheDir = f.flags.cacheDir
	}
	if changed("cache-ttl") {
		cfg.CacheTTL = config.Duration(f.flags.cacheTTL)
	}
	if changed("timeout") {
		cfg.Timeout = config.Duration(f.flags.timeout)
	}
	if changed("rate-limit") {
		cfg.RateLimit = config.Duration(f.flags.rateLimit)
	}
	if f.flags.userAgent != "" {
		cfg.UserAgent = f.flags.userAgent
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = version.UserAgent()
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	f.cfg = cfg

	format, err := output.ParseFormat(cfg.Output)
	if err != nil {
		return err
	}
	colorOn := output.ColorEnabled(cfg.Color, os.Stdout)
	f.printer = output.New(format, colorOn)
	f.printer.NoHeaders = f.flags.noHeaders

	f.store = auth.NewStore(cfg.KeyringService)

	if !f.flags.noCache {
		c, err := cache.New(cfg.CacheDir, cfg.CacheTTL.Duration())
		if err != nil {
			// A broken cache directory should degrade to no caching, not abort.
			if !f.flags.quiet {
				f.printer.Warn("caching disabled: %v", err)
			}
		} else {
			f.cache = c
		}
	}
	return nil
}

func (f *Factory) cacheMode() cache.Mode {
	switch {
	case f.flags.offline:
		return cache.ModeOffline
	case f.flags.noCache:
		return cache.ModeBypass
	case f.flags.refresh:
		return cache.ModeRefresh
	default:
		return cache.ModeDefault
	}
}

// baseTransport is the round tripper the API client sits on: the shared HTTP
// transport, wrapped in the on-disk cache when caching is enabled.
func (f *Factory) baseTransport() http.RoundTripper {
	if f.cache == nil {
		return http.DefaultTransport
	}
	return f.cache.Transport(http.DefaultTransport, f.cacheMode())
}

func (f *Factory) clientOptions() []api.Option {
	transport := f.baseTransport()
	return []api.Option{
		api.WithBaseURL(f.cfg.BaseURL),
		api.WithLocale(f.cfg.Locale),
		api.WithUserAgent(f.cfg.UserAgent),
		api.WithTransport(transport),
		api.WithTimeout(f.cfg.Timeout.Duration()),
		api.WithRateLimit(f.cfg.RateLimit.Duration()),
		api.WithRetryPolicy(api.RetryPolicy{
			MaxAttempts: f.cfg.MaxRetries,
			BaseDelay:   400 * time.Millisecond,
			MaxDelay:    5 * time.Second,
		}),
	}
}

// Client returns a client for the public endpoints.
func (f *Factory) Client() (*api.Client, error) {
	if f.client != nil {
		return f.client, nil
	}
	c, err := api.New(f.clientOptions()...)
	if err != nil {
		return nil, err
	}
	f.client = c
	return c, nil
}

// AuthClient returns a client carrying the stored OAuth2 credentials.
func (f *Factory) AuthClient(ctx context.Context) (*api.Client, error) {
	if f.authClient != nil {
		return f.authClient, nil
	}
	creds, err := f.store.LoadClient()
	if err != nil {
		if errors.Is(err, auth.ErrNoCredentials) {
			return nil, errNotLoggedIn("no OAuth2 client is configured")
		}
		return nil, err
	}
	tok, err := f.store.LoadToken()
	if err != nil {
		if errors.Is(err, auth.ErrNoCredentials) {
			return nil, errNotLoggedIn("you are not signed in")
		}
		return nil, err
	}

	cfg, err := auth.Config(f.cfg.BaseURL, creds)
	if err != nil {
		return nil, err
	}
	src := auth.PersistingTokenSource(f.store, cfg.TokenSource(ctx, tok), tok)

	opts := append(f.clientOptions(), api.WithTokenSource(src))
	c, err := api.New(opts...)
	if err != nil {
		return nil, err
	}
	f.authClient = c
	return c, nil
}

// TokenSource returns a refreshing, self-persisting token source.
func (f *Factory) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	creds, err := f.store.LoadClient()
	if err != nil {
		return nil, err
	}
	tok, err := f.store.LoadToken()
	if err != nil {
		return nil, err
	}
	cfg, err := auth.Config(f.cfg.BaseURL, creds)
	if err != nil {
		return nil, err
	}
	return auth.PersistingTokenSource(f.store, cfg.TokenSource(ctx, tok), tok), nil
}

func errNotLoggedIn(reason string) error {
	return fmt.Errorf("%w: %s.\n\nMarvelCDB issues OAuth2 credentials to registered applications by email.\n"+
		"Once you have a client id and secret:\n"+
		"  marvelcdb auth client set --id <id> --secret <secret>\n"+
		"  marvelcdb auth login", api.ErrUnauthorized, reason)
}

// NewRootCommand builds the command tree.
func NewRootCommand() *cobra.Command {
	f := &Factory{}

	cmd := &cobra.Command{
		Use:   "marvelcdb",
		Short: "Query MarvelCDB and manage your Marvel Champions decks",
		Long: strings.TrimSpace(`
marvelcdb is a command-line client for MarvelCDB, the Marvel Champions card
database at https://marvelcdb.com.

Card, pack, faction, FAQ and decklist lookups need no account. Managing your own
decks needs OAuth2 credentials, which MarvelCDB issues to registered
applications; see "marvelcdb auth client --help".

Responses are cached on disk and revalidated with conditional requests, so
repeated queries stay fast and stay polite to a community-run site.`),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Commands that never touch config or the network skip setup.
			if cmd.Name() == "help" || cmd.Name() == "completion" {
				return nil
			}
			return f.resolve(flagsChangedOn(cmd))
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	pf := cmd.PersistentFlags()
	pf.StringVar(&f.flags.configPath, "config", "", "config file (default: OS config dir/marvelcdb-cli/config.yaml)")
	pf.StringVar(&f.flags.baseURL, "base-url", "", "MarvelCDB origin (default: https://marvelcdb.com)")
	pf.StringVar(&f.flags.locale, "locale", "", "translation to request: "+strings.Join(api.Locales, ", "))
	pf.StringVarP(&f.flags.outputFmt, "output", "o", "", "output format: "+strings.Join(output.Formats(), ", "))
	pf.StringVar(&f.flags.color, "color", "", "colorise output: auto, always, never")
	pf.BoolVar(&f.flags.noHeaders, "no-headers", false, "omit the header row in table, csv and tsv output")
	pf.StringVar(&f.flags.cacheDir, "cache-dir", "", "directory for the HTTP cache")
	pf.DurationVar(&f.flags.cacheTTL, "cache-ttl", 0, "how long a cached response stays fresh (default: 1h)")
	pf.BoolVar(&f.flags.noCache, "no-cache", false, "neither read nor write the cache")
	pf.BoolVar(&f.flags.refresh, "refresh", false, "revalidate cached responses even when fresh")
	pf.BoolVar(&f.flags.offline, "offline", false, "serve only from cache; fail if a response is missing")
	pf.DurationVar(&f.flags.timeout, "timeout", 0, "per-request timeout (default: 60s)")
	pf.DurationVar(&f.flags.rateLimit, "rate-limit", 0, "minimum interval between requests (default: 100ms)")
	pf.StringVar(&f.flags.userAgent, "user-agent", "", "User-Agent header to send")
	pf.BoolVarP(&f.flags.quiet, "quiet", "q", false, "suppress advisory messages on stderr")

	cmd.AddCommand(
		newCardsCommand(f),
		newPacksCommand(f),
		newFactionsCommand(f),
		newFaqCommand(f),
		newTaboosCommand(f),
		newDecklistsCommand(f),
		newDecksCommand(f),
		newMyDecksCommand(f),
		newAuthCommand(f),
		newCacheCommand(f),
		newConfigCommand(f),
		newVersionCommand(),
	)

	registerGlobalCompletions(cmd)
	return cmd
}

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context) int {
	root := NewRootCommand()
	if err := root.ExecuteContext(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			_, _ = fmt.Fprintln(os.Stderr, "cancelled")
			return 130
		}
		_, _ = fmt.Fprintln(os.Stderr, "error: "+err.Error())
		return exitCode(err)
	}
	return 0
}

// exitCode maps failure kinds onto conventional exit statuses so scripts can
// branch without parsing messages.
func exitCode(err error) int {
	switch {
	case api.IsNotFound(err):
		return 4
	case api.IsUnauthorized(err):
		return 3
	case errors.Is(err, cache.ErrOffline):
		return 5
	default:
		return 1
	}
}
