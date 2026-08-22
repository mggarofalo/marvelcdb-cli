package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mggarofalo/marvelcdb-cli/internal/api"
	"github.com/mggarofalo/marvelcdb-cli/internal/output"
)

func newDecklistsCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "decklists",
		Aliases: []string{"decklist"},
		Short:   "Browse published decklists",
	}
	cmd.AddCommand(
		newDecklistsGetCommand(f),
		newDecklistsByDateCommand(f),
		newDecklistsPopularCommand(f),
	)
	return cmd
}

func newDecklistsGetCommand(f *Factory) *cobra.Command {
	var withCards bool
	cmd := &cobra.Command{
		Use:     "get <id>...",
		Aliases: []string{"show"},
		Short:   "Show a published decklist by id",
		Example: "  marvelcdb decklists get 35109\n  marvelcdb decklists get 35109 -o text --with-cards",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.Client()
			if err != nil {
				return err
			}
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			decks := make([]api.Deck, 0, len(ids))
			var failures []error
			for _, id := range ids {
				d, err := client.Decklist(cmd.Context(), id)
				if err != nil {
					failures = append(failures, err)
					continue
				}
				decks = append(decks, *d)
			}
			if len(decks) > 0 {
				if err := printDecks(cmd.Context(), f, client, decks, withCards); err != nil {
					return err
				}
			}
			return errors.Join(failures...)
		},
	}
	cmd.Flags().BoolVar(&withCards, "with-cards", false, "resolve card codes to names (downloads the card list once)")
	return cmd
}

func newDecklistsByDateCommand(f *Factory) *cobra.Command {
	var withCards bool
	var limit int

	cmd := &cobra.Command{
		Use:     "by-date <date>",
		Aliases: []string{"date"},
		Short:   "List decklists published on one day",
		Long: strings.TrimSpace(`
List every decklist published on a given day.

The date may be YYYY-MM-DD, or the words "today" or "yesterday", which are
resolved in your local time zone.`),
		Example: "  marvelcdb decklists by-date 2024-01-15\n  marvelcdb decklists by-date today",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			date, err := resolveDate(args[0], time.Now())
			if err != nil {
				return err
			}
			client, err := f.Client()
			if err != nil {
				return err
			}
			decks, err := client.DecklistsByDate(cmd.Context(), date)
			if err != nil {
				return err
			}
			if limit > 0 && len(decks) > limit {
				decks = decks[:limit]
			}
			if len(decks) == 0 && !f.flags.quiet {
				f.printer.Warn("no decklists published on %s", date)
			}
			return printDecks(cmd.Context(), f, client, decks, withCards)
		},
	}
	cmd.Flags().BoolVar(&withCards, "with-cards", false, "resolve card codes to names in text output")
	cmd.Flags().IntVar(&limit, "limit", 0, "stop after this many decklists (0 for all)")
	return cmd
}

func newDecklistsPopularCommand(f *Factory) *cobra.Command {
	var withCards bool
	cmd := &cobra.Command{
		Use:   "popular",
		Short: "List the decklists MarvelCDB is currently featuring",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := f.Client()
			if err != nil {
				return err
			}
			popular, err := client.PopularDecklists(cmd.Context())
			if err != nil {
				return err
			}
			decks := make([]api.Deck, 0, len(popular))
			for _, p := range popular {
				decks = append(decks, p.Decklist)
			}
			// The wrapper adds only presentation data, so structured output
			// shows the decklists themselves.
			return printDecks(cmd.Context(), f, client, decks, withCards)
		},
	}
	cmd.Flags().BoolVar(&withCards, "with-cards", false, "resolve card codes to names in text output")
	return cmd
}

func newDecksCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "decks",
		Aliases: []string{"deck"},
		Short:   "Look up shared decks",
		Long: "Read decks that someone has shared but not published as a decklist.\n" +
			"For your own decks, see `marvelcdb mydecks`.",
	}
	cmd.AddCommand(newDecksGetCommand(f))
	return cmd
}

func newDecksGetCommand(f *Factory) *cobra.Command {
	var withCards bool
	cmd := &cobra.Command{
		Use:     "get <id>...",
		Aliases: []string{"show"},
		Short:   "Show a shared deck by id",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.Client()
			if err != nil {
				return err
			}
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			decks := make([]api.Deck, 0, len(ids))
			var failures []error
			for _, id := range ids {
				d, err := client.PublicDeck(cmd.Context(), id)
				if err != nil {
					failures = append(failures, err)
					continue
				}
				decks = append(decks, *d)
			}
			if len(decks) > 0 {
				if err := printDecks(cmd.Context(), f, client, decks, withCards); err != nil {
					return err
				}
			}
			return errors.Join(failures...)
		},
	}
	cmd.Flags().BoolVar(&withCards, "with-cards", false, "resolve card codes to names in text output")
	return cmd
}

// printDecks renders a deck slice, building the card index only when the chosen
// format will actually use it.
func printDecks(ctx context.Context, f *Factory, client *api.Client, decks []api.Deck, withCards bool) error {
	var idx cardIndex
	if withCards && f.printer.Format == output.FormatText {
		idx = buildCardIndex(ctx, client)
		if idx == nil && !f.flags.quiet {
			f.printer.Warn("could not load the card list; showing codes only")
		}
	}

	t := deckTable(decks)
	if f.printer.Format == output.FormatText {
		sheets := make([]string, 0, len(decks))
		for _, d := range decks {
			sheets = append(sheets, deckSheet(d, idx))
		}
		t.Text = strings.Join(sheets, "\n"+strings.Repeat("─", 60)+"\n\n")
	}

	var payload any = decks
	if len(decks) == 1 {
		payload = decks[0]
	}
	return f.printer.Print(payload, t)
}

func parseIDs(args []string) ([]int, error) {
	ids := make([]int, 0, len(args))
	for _, a := range args {
		// Accept a MarvelCDB URL as well as a bare id.
		trimmed := a
		if i := strings.LastIndex(a, "/"); i >= 0 {
			trimmed = a[i+1:]
		}
		id, err := strconv.Atoi(trimmed)
		if err != nil {
			return nil, fmt.Errorf("%q is not a numeric id", a)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// resolveDate turns a user-supplied date into the YYYY-MM-DD the API requires.
func resolveDate(input string, now time.Time) (string, error) {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "today":
		return now.Format("2006-01-02"), nil
	case "yesterday":
		return now.AddDate(0, 0, -1).Format("2006-01-02"), nil
	}
	if _, err := time.Parse("2006-01-02", input); err != nil {
		return "", fmt.Errorf("date %q must be YYYY-MM-DD, \"today\" or \"yesterday\"", input)
	}
	return input, nil
}
