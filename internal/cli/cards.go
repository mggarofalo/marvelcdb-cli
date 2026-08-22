package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mggarofalo/marvelcdb-cli/internal/api"
	"github.com/mggarofalo/marvelcdb-cli/internal/output"
)

func newCardsCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cards",
		Aliases: []string{"card"},
		Short:   "Browse and search the card database",
	}
	cmd.AddCommand(
		newCardsListCommand(f),
		newCardsGetCommand(f),
		newCardsSearchCommand(f),
		newCardsImageCommand(f),
	)
	return cmd
}

type cardsListOptions struct {
	filter    cardFilter
	encounter bool
	sortBy    string
	reverse   bool
	limit     int
	wide      bool
	unique    bool
	permanent bool
}

func newCardsListCommand(f *Factory) *cobra.Command {
	opts := &cardsListOptions{}
	var minCost, maxCost int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cards, with optional filters",
		Long: strings.TrimSpace(`
List cards from the MarvelCDB corpus.

MarvelCDB has no server-side query language, so the whole corpus is downloaded
once and filtered locally. It is cached afterwards, which makes repeated
queries fast; pass --refresh to check for new cards.

Repeating a filter flag widens the match: --faction leadership --faction justice
returns cards in either aspect. Different flags narrow it.`),
		Example: strings.TrimSpace(`
  # Every Leadership ally that costs 2 or less
  marvelcdb cards list --faction leadership --type ally --max-cost 2

  # Spider-Man's signature cards
  marvelcdb cards list --set spider_man

  # Aerial cards, as JSON
  marvelcdb cards list --trait Aerial -o json`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("min-cost") {
				opts.filter.minCost = &minCost
			}
			if cmd.Flags().Changed("max-cost") {
				opts.filter.maxCost = &maxCost
			}
			if cmd.Flags().Changed("unique") {
				opts.filter.unique = &opts.unique
			}
			if cmd.Flags().Changed("permanent") {
				opts.filter.permanent = &opts.permanent
			}
			return runCardsList(cmd.Context(), f, opts)
		},
	}

	fl := cmd.Flags()
	fl.StringSliceVar(&opts.filter.packs, "pack", nil, "pack code (repeatable)")
	fl.StringSliceVar(&opts.filter.types, "type", nil, "type code such as ally, event, upgrade (repeatable)")
	fl.StringSliceVar(&opts.filter.factions, "faction", nil, "faction code such as leadership, hero, basic (repeatable)")
	fl.StringSliceVar(&opts.filter.sets, "set", nil, "card set code such as spider_man (repeatable)")
	fl.StringSliceVar(&opts.filter.traits, "trait", nil, "trait such as Avenger (repeatable)")
	fl.StringVar(&opts.filter.name, "name", "", "substring of the card name")
	fl.StringVar(&opts.filter.text, "text", "", "substring of the card text")
	fl.StringVar(&opts.filter.search, "search", "", "substring of name, text, traits or flavour")
	fl.IntSliceVar(&opts.filter.costs, "cost", nil, "exact resource cost (repeatable)")
	fl.IntVar(&minCost, "min-cost", 0, "minimum resource cost")
	fl.IntVar(&maxCost, "max-cost", 0, "maximum resource cost")
	fl.BoolVar(&opts.unique, "unique", false, "only unique cards (--unique=false for only non-unique)")
	fl.BoolVar(&opts.permanent, "permanent", false, "only permanent cards (--permanent=false to invert)")
	fl.BoolVar(&opts.encounter, "encounter", false, "include encounter and villain cards")
	fl.BoolVar(&opts.filter.includeDuplicates, "duplicates", false, "include reprints of cards from earlier packs")
	fl.BoolVar(&opts.filter.hideSpoilers, "hide-spoilers", false, "exclude cards MarvelCDB flags as spoilers")
	fl.StringVar(&opts.sortBy, "sort", "code", "sort by: "+strings.Join(sortFields, ", "))
	fl.BoolVar(&opts.reverse, "reverse", false, "reverse the sort order")
	fl.IntVar(&opts.limit, "limit", 0, "stop after this many cards (0 for all)")
	fl.BoolVar(&opts.wide, "wide", false, "show extra columns in table output")

	registerCardCompletions(f, cmd)
	return cmd
}

func runCardsList(ctx context.Context, f *Factory, opts *cardsListOptions) error {
	client, err := f.Client()
	if err != nil {
		return err
	}

	var cards []api.Card
	// A single --pack is far cheaper to fetch on its own than the full corpus.
	if len(opts.filter.packs) == 1 {
		cards, err = client.CardsByPack(ctx, opts.filter.packs[0])
	} else {
		cards, err = client.Cards(ctx, opts.encounter)
	}
	if err != nil {
		return err
	}

	filtered := opts.filter.apply(cards)
	if err := sortCards(filtered, opts.sortBy, opts.reverse); err != nil {
		return err
	}
	if opts.limit > 0 && len(filtered) > opts.limit {
		filtered = filtered[:opts.limit]
	}

	if len(filtered) == 0 && !f.flags.quiet {
		f.printer.Warn("no cards matched")
	}
	return f.printer.Print(filtered, cardTable(filtered, opts.wide))
}

func newCardsSearchCommand(f *Factory) *cobra.Command {
	opts := &cardsListOptions{}
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search card names, text and traits",
		Long: "Search is shorthand for `cards list --search <query>`; the query is matched\n" +
			"case-insensitively against the name, text, traits and flavour of every card.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.filter.search = args[0]
			return runCardsList(cmd.Context(), f, opts)
		},
	}
	fl := cmd.Flags()
	fl.StringSliceVar(&opts.filter.factions, "faction", nil, "faction code (repeatable)")
	fl.StringSliceVar(&opts.filter.types, "type", nil, "type code (repeatable)")
	fl.StringSliceVar(&opts.filter.packs, "pack", nil, "pack code (repeatable)")
	fl.BoolVar(&opts.encounter, "encounter", false, "include encounter and villain cards")
	fl.BoolVar(&opts.filter.includeDuplicates, "duplicates", false, "include reprints")
	fl.StringVar(&opts.sortBy, "sort", "code", "sort by: "+strings.Join(sortFields, ", "))
	fl.BoolVar(&opts.reverse, "reverse", false, "reverse the sort order")
	fl.IntVar(&opts.limit, "limit", 0, "stop after this many cards (0 for all)")
	fl.BoolVar(&opts.wide, "wide", false, "show extra columns in table output")
	registerCardCompletions(f, cmd)
	return cmd
}

func newCardsGetCommand(f *Factory) *cobra.Command {
	var withLinked bool

	cmd := &cobra.Command{
		Use:     "get <code>...",
		Aliases: []string{"show"},
		Short:   "Show one or more cards by code",
		Long: strings.TrimSpace(`
Show full details for cards, addressed by MarvelCDB code (for example 01001a).

Note that MarvelCDB answers an unknown code with HTTP 500 rather than 404; the
CLI reports that as "not found" and exits 4.`),
		Example: "  marvelcdb cards get 01001a\n  marvelcdb cards get 01050 01051 -o json",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.Client()
			if err != nil {
				return err
			}

			cards := make([]api.Card, 0, len(args))
			var failures []error
			for _, code := range args {
				card, err := client.Card(cmd.Context(), code)
				if err != nil {
					failures = append(failures, err)
					continue
				}
				cards = append(cards, *card)
				if withLinked && card.LinkedCard != nil {
					cards = append(cards, *card.LinkedCard)
				}
			}

			// Report what was found before failing, so a typo in a long list
			// does not throw away the rest of the work.
			if len(cards) > 0 {
				var payload any = cards
				if len(cards) == 1 {
					payload = cards[0]
				}
				if err := f.printer.Print(payload, cardDetailTable(cards)); err != nil {
					return err
				}
			}
			return errors.Join(failures...)
		},
	}
	cmd.Flags().BoolVar(&withLinked, "with-linked", false, "also show the card on the reverse side, when there is one")
	registerCardCompletions(f, cmd)
	return cmd
}

func newCardsImageCommand(f *Factory) *cobra.Command {
	var back bool
	cmd := &cobra.Command{
		Use:   "image <code>...",
		Short: "Print the image URL for a card",
		Long:  "Print absolute image URLs, ready to pipe into a downloader.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.Client()
			if err != nil {
				return err
			}
			t := &output.Table{Headers: []string{"CODE", "IMAGE"}}
			type imageRow struct {
				Code  string `json:"code"`
				Image string `json:"image"`
			}
			var rows []imageRow
			var failures []error
			for _, code := range args {
				card, err := client.Card(cmd.Context(), code)
				if err != nil {
					failures = append(failures, err)
					continue
				}
				src := card.ImageSrc
				if back {
					src = card.BackImageSrc
				}
				if src == "" {
					failures = append(failures, fmt.Errorf("card %s has no %s image on MarvelCDB", code, sideName(back)))
					continue
				}
				abs := absoluteURL(f.cfg.BaseURL, src)
				rows = append(rows, imageRow{Code: card.Code, Image: abs})
				t.Rows = append(t.Rows, []string{card.Code, abs})
			}
			if len(rows) > 0 {
				if err := f.printer.Print(rows, t); err != nil {
					return err
				}
			}
			return errors.Join(failures...)
		},
	}
	cmd.Flags().BoolVar(&back, "back", false, "print the reverse-side image instead")
	registerCardCompletions(f, cmd)
	return cmd
}

func sideName(back bool) string {
	if back {
		return "back"
	}
	return "front"
}

func absoluteURL(baseURL, path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return strings.TrimSuffix(baseURL, "/") + "/" + strings.TrimPrefix(path, "/")
}

// cardDetailTable renders full card details: a compact table plus a card sheet
// for the "text" format.
func cardDetailTable(cards []api.Card) *output.Table {
	t := cardTable(cards, true)
	sheets := make([]string, 0, len(cards))
	for _, c := range cards {
		sheets = append(sheets, cardSheet(c))
	}
	t.Text = strings.Join(sheets, "\n"+strings.Repeat("─", 60)+"\n\n")
	return t
}

func cardSheet(c api.Card) string {
	var b strings.Builder

	title := c.Name
	if c.Subname != "" {
		title += " — " + c.Subname
	}
	fmt.Fprintf(&b, "%s  [%s]\n", title, c.Code)

	line := strings.TrimSpace(strings.Join(dedupe(nonEmpty(c.TypeName, c.FactionName, c.CardSetName)), " · "))
	if line != "" {
		fmt.Fprintf(&b, "%s\n", line)
	}
	if traits := c.Traits; traits != "" {
		fmt.Fprintf(&b, "%s\n", traits)
	}
	b.WriteString("\n")

	writeStat(&b, "Cost", c.Cost)
	writeStat(&b, "Attack", c.Attack)
	writeStat(&b, "Thwart", c.Thwart)
	writeStat(&b, "Defense", c.Defense)
	writeStat(&b, "Recover", c.Recover)
	writeStat(&b, "Health", c.Health)
	writeStat(&b, "Hand size", c.HandSize)
	writeStat(&b, "Scheme", c.Scheme)
	writeStat(&b, "Boost", c.Boost)
	writeStat(&b, "Base threat", c.BaseThreat)
	writeStat(&b, "Escalation", c.EscalationThreat)
	if r := c.Resources(); r > 0 {
		fmt.Fprintf(&b, "Resources:   %s\n", resourceLine(c))
	}
	writeStat(&b, "Deck limit", c.DeckLimit)

	if text := output.StripHTML(c.Text); text != "" {
		fmt.Fprintf(&b, "\n%s\n", text)
	}
	if c.BackName != "" || c.BackText != "" {
		b.WriteString("\n--- reverse ---\n")
		if c.BackName != "" {
			fmt.Fprintf(&b, "%s\n", c.BackName)
		}
		if back := output.StripHTML(c.BackText); back != "" {
			fmt.Fprintf(&b, "%s\n", back)
		}
	}
	if flavor := output.StripHTML(c.Flavor); flavor != "" {
		fmt.Fprintf(&b, "\n%s\n", flavor)
	}
	if c.Errata != "" {
		fmt.Fprintf(&b, "\nErrata: %s\n", output.StripHTML(c.Errata))
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "Pack:        %s\n", c.PackName)
	if c.Illustrator != "" {
		fmt.Fprintf(&b, "Illustrator: %s\n", c.Illustrator)
	}
	if c.LinkedToCode != "" {
		fmt.Fprintf(&b, "Linked to:   %s [%s]\n", c.LinkedToName, c.LinkedToCode)
	}
	if c.DuplicateOfCode != "" {
		fmt.Fprintf(&b, "Reprint of:  %s [%s]\n", c.DuplicateOfName, c.DuplicateOfCode)
	}
	if c.URL != "" {
		fmt.Fprintf(&b, "URL:         %s\n", c.URL)
	}
	return b.String()
}

func writeStat(b *strings.Builder, label string, v *int) {
	if v == nil {
		return
	}
	fmt.Fprintf(b, "%-12s %d\n", label+":", *v)
}

func resourceLine(c api.Card) string {
	parts := make([]string, 0, 4)
	for _, r := range []struct {
		name  string
		count *int
	}{
		{"physical", c.ResourcePhysical},
		{"mental", c.ResourceMental},
		{"energy", c.ResourceEnergy},
		{"wild", c.ResourceWild},
	} {
		if n := intValue(r.count); n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, r.name))
		}
	}
	return strings.Join(parts, ", ")
}

// dedupe drops repeats, which keeps lines like "Hero · Hero" from happening.
func dedupe(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		key := strings.ToLower(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
