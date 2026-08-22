package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mggarofalo/marvelcdb-cli/internal/output"
)

func newCacheCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Inspect and manage the on-disk HTTP cache",
		Long: strings.TrimSpace(`
Inspect and manage the HTTP cache.

MarvelCDB serves the whole card corpus as one document, so the CLI caches
responses on disk and revalidates them with conditional requests. An unchanged
corpus costs a 304 rather than several megabytes.

Authenticated responses are never cached.`),
	}
	cmd.AddCommand(
		newCacheInfoCommand(f),
		newCacheClearCommand(f),
		newCachePruneCommand(f),
		newCacheWarmCommand(f),
	)
	return cmd
}

func newCacheInfoCommand(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "info",
		Aliases: []string{"status"},
		Short:   "Show where the cache lives and what is in it",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if f.cache == nil {
				return errors.New("caching is disabled for this invocation")
			}
			stats, err := f.cache.Stats()
			if err != nil {
				return err
			}
			t := &output.Table{
				Headers: []string{"SETTING", "VALUE"},
				Rows: [][]string{
					{"directory", stats.Dir},
					{"entries", fmt.Sprintf("%d", stats.Entries)},
					{"size", humanBytes(stats.Bytes)},
					{"ttl", f.cfg.CacheTTL.Duration().String()},
					{"oldest entry", formatTime(stats.Oldest)},
					{"newest entry", formatTime(stats.Newest)},
				},
			}
			return f.printer.Print(stats, t)
		},
	}
}

func newCacheClearCommand(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Delete every cached response",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if f.cache == nil {
				return errors.New("caching is disabled for this invocation")
			}
			n, err := f.cache.Clear()
			if err != nil {
				return err
			}
			if !f.flags.quiet {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Removed %d cached response(s) from %s.\n", n, f.cache.Dir())
			}
			return nil
		},
	}
}

func newCachePruneCommand(f *Factory) *cobra.Command {
	var olderThan time.Duration
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete cached responses older than a cutoff",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if f.cache == nil {
				return errors.New("caching is disabled for this invocation")
			}
			n, err := f.cache.Prune(time.Now().Add(-olderThan))
			if err != nil {
				return err
			}
			if !f.flags.quiet {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Removed %d cached response(s) older than %s.\n", n, olderThan)
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&olderThan, "older-than", 30*24*time.Hour, "age cutoff")
	return cmd
}

func newCacheWarmCommand(f *Factory) *cobra.Command {
	var encounter bool
	cmd := &cobra.Command{
		Use:   "warm",
		Short: "Pre-fetch the card, pack and faction lists",
		Long: "Download and cache everything the local filters need, so later queries —\n" +
			"and shell completions — run without waiting on the network.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := f.Client()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			type step struct {
				name string
				run  func() (int, error)
			}
			steps := []step{
				{"packs", func() (int, error) { p, err := client.Packs(ctx); return len(p), err }},
				{"factions", func() (int, error) { x, err := client.Factions(ctx); return len(x), err }},
				{"cards", func() (int, error) { c, err := client.Cards(ctx, false); return len(c), err }},
			}
			if encounter {
				steps = append(steps, step{"cards (with encounter)", func() (int, error) {
					c, err := client.Cards(ctx, true)
					return len(c), err
				}})
			}

			t := &output.Table{Headers: []string{"RESOURCE", "RECORDS"}}
			results := map[string]int{}
			for _, s := range steps {
				n, err := s.run()
				if err != nil {
					return fmt.Errorf("warm %s: %w", s.name, err)
				}
				results[s.name] = n
				t.Rows = append(t.Rows, []string{s.name, fmt.Sprintf("%d", n)})
			}
			return f.printer.Print(results, t)
		},
	}
	cmd.Flags().BoolVar(&encounter, "encounter", false, "also cache the corpus including encounter cards")
	return cmd
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format(time.RFC3339)
}
