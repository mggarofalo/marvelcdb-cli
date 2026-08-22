package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mggarofalo/marvelcdb-cli/internal/api"
	"github.com/mggarofalo/marvelcdb-cli/internal/output"
)

func newMyDecksCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "mydecks",
		Aliases: []string{"mydeck"},
		Short:   "Manage your own decks (requires sign-in)",
		Long: strings.TrimSpace(`
Read and edit the decks on your MarvelCDB account.

These commands use the OAuth2 endpoints, so they need credentials: see
"marvelcdb auth --help".`),
	}
	cmd.AddCommand(
		newMyDecksListCommand(f),
		newMyDecksGetCommand(f),
		newMyDecksNewCommand(f),
		newMyDecksSaveCommand(f),
		newMyDecksDeleteCommand(f),
		newMyDecksPublishCommand(f),
	)
	return cmd
}

func newMyDecksListCommand(f *Factory) *cobra.Command {
	var hero string
	return &cobra.Command{
		Use:   "list",
		Short: "List your decks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := f.AuthClient(cmd.Context())
			if err != nil {
				return err
			}
			decks, err := client.MyDecks(cmd.Context())
			if err != nil {
				return err
			}
			if hero != "" {
				kept := make([]api.Deck, 0, len(decks))
				for _, d := range decks {
					if strings.EqualFold(d.HeroCode, hero) || containsFold(d.HeroName, hero) {
						kept = append(kept, d)
					}
				}
				decks = kept
			}
			if len(decks) == 0 && !f.flags.quiet {
				f.printer.Warn("no decks found")
			}
			return f.printer.Print(decks, deckTable(decks))
		},
	}
}

func newMyDecksGetCommand(f *Factory) *cobra.Command {
	var withCards bool
	cmd := &cobra.Command{
		Use:     "get <id>...",
		Aliases: []string{"show", "load"},
		Short:   "Show one of your decks",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.AuthClient(cmd.Context())
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
				d, err := client.LoadDeck(cmd.Context(), id)
				if err != nil {
					failures = append(failures, err)
					continue
				}
				decks = append(decks, *d)
			}
			if len(decks) > 0 {
				// Card names come from the public endpoints, which the
				// unauthenticated client reads straight from the cache.
				public, perr := f.Client()
				if perr != nil {
					return perr
				}
				if err := printDecks(cmd.Context(), f, public, decks, withCards); err != nil {
					return err
				}
			}
			return errors.Join(failures...)
		},
	}
	cmd.Flags().BoolVar(&withCards, "with-cards", false, "resolve card codes to names in text output")
	return cmd
}

func newMyDecksNewCommand(f *Factory) *cobra.Command {
	var hero, name, meta, aspect string

	cmd := &cobra.Command{
		Use:   "new --hero <code>",
		Short: "Create a deck",
		Long: strings.TrimSpace(`
Create an empty deck for a hero. MarvelCDB pre-fills it with that hero's
required cards; everything else you add with "mydecks save".`),
		Example: "  marvelcdb mydecks new --hero 01001a --name \"Webhead\" --aspect justice",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			metaJSON, err := resolveMeta(meta, aspect)
			if err != nil {
				return err
			}
			client, err := f.AuthClient(cmd.Context())
			if err != nil {
				return err
			}
			res, err := client.NewDeck(cmd.Context(), api.NewDeckRequest{
				Hero: hero, Name: name, Meta: metaJSON,
			})
			if err != nil {
				return err
			}
			return printMutation(f, res, "created deck")
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&hero, "hero", "", "hero identity card code, for example 01001a (required)")
	fl.StringVar(&name, "name", "", "deck name (defaults to the hero's name)")
	fl.StringVar(&aspect, "aspect", "", "aspect: "+strings.Join(api.Aspects, ", "))
	fl.StringVar(&meta, "meta", "", "raw meta JSON, if you need more than --aspect")
	_ = cmd.MarkFlagRequired("hero")
	_ = cmd.RegisterFlagCompletionFunc("aspect", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return api.Aspects, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("hero", func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		cards := f.completeCards(cmd)
		out := make([]string, 0, 64)
		for _, c := range cards {
			if c.TypeCode == "hero" {
				out = append(out, c.Code+"\t"+c.Name)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

type saveOptions struct {
	slots        []string
	slotsFile    string
	ignored      []string
	problem      string
	name         string
	description  string
	tags         string
	meta         string
	aspect       string
	decklistID   int
	xpSpent      int
	xpAdjustment int
	dryRun       bool
}

func newMyDecksSaveCommand(f *Factory) *cobra.Command {
	opts := &saveOptions{}

	cmd := &cobra.Command{
		Use:   "save <id>",
		Short: "Write deck contents",
		Long: strings.TrimSpace(`
Replace the contents of one of your decks.

MarvelCDB has no "add one card" operation: a save sends the complete slot list
and replaces what was stored. Read the deck first, edit it, then send it back:

  marvelcdb mydecks get 12345 -o json > deck.json
  # edit the "slots" object
  marvelcdb mydecks save 12345 --slots-file deck.json`),
		Example: strings.TrimSpace(`
  marvelcdb mydecks save 12345 --slot 01050=2 --slot 01053=1
  marvelcdb mydecks save 12345 --slots-file deck.json --problem too_few_cards`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			slots, err := collectSlots(opts.slots, opts.slotsFile)
			if err != nil {
				return err
			}
			ignored, err := collectSlots(opts.ignored, "")
			if err != nil {
				return err
			}
			metaJSON, err := resolveMeta(opts.meta, opts.aspect)
			if err != nil {
				return err
			}

			req := api.SaveDeckRequest{
				Slots:       slots,
				Ignored:     ignored,
				Problem:     opts.problem,
				Name:        opts.name,
				Description: opts.description,
				Tags:        opts.tags,
				Meta:        metaJSON,
				DecklistID:  opts.decklistID,
			}
			if cmd.Flags().Changed("xp-spent") {
				req.XPSpent = &opts.xpSpent
			}
			if cmd.Flags().Changed("xp-adjustment") {
				req.XPAdjustment = &opts.xpAdjustment
			}

			if opts.dryRun {
				return f.printer.Print(map[string]any{
					"deck_id": ids[0],
					"slots":   slots,
					"ignored": ignored,
					"problem": opts.problem,
					"name":    opts.name,
					"meta":    metaJSON,
				}, &output.Table{
					Headers: []string{"FIELD", "VALUE"},
					Rows: [][]string{
						{"deck_id", strconv.Itoa(ids[0])},
						{"slots", fmt.Sprintf("%d distinct cards, %d total", len(slots), totalQty(slots))},
						{"problem", opts.problem},
						{"name", opts.name},
						{"meta", metaJSON},
					},
				})
			}

			client, err := f.AuthClient(cmd.Context())
			if err != nil {
				return err
			}
			res, err := client.SaveDeck(cmd.Context(), ids[0], req)
			if err != nil {
				return err
			}
			return printMutation(f, res, "saved deck")
		},
	}

	fl := cmd.Flags()
	fl.StringArrayVar(&opts.slots, "slot", nil, "card slot as CODE=QUANTITY (repeatable)")
	fl.StringVar(&opts.slotsFile, "slots-file", "", "JSON file holding the slots, or a deck object with a \"slots\" key; - for stdin")
	fl.StringArrayVar(&opts.ignored, "ignore-limit", nil, "slot exempt from the deck limit, as CODE=QUANTITY (repeatable)")
	fl.StringVar(&opts.problem, "problem", "", "deck problem code: "+strings.Join(api.ValidProblems, ", "))
	fl.StringVar(&opts.name, "name", "", "rename the deck")
	fl.StringVar(&opts.description, "description", "", "deck description in Markdown")
	fl.StringVar(&opts.tags, "tags", "", "space-separated tags")
	fl.StringVar(&opts.aspect, "aspect", "", "aspect: "+strings.Join(api.Aspects, ", "))
	fl.StringVar(&opts.meta, "meta", "", "raw meta JSON, if you need more than --aspect")
	fl.IntVar(&opts.decklistID, "decklist-id", 0, "decklist this deck was copied from")
	fl.IntVar(&opts.xpSpent, "xp-spent", 0, "experience spent (upgraded decks only)")
	fl.IntVar(&opts.xpAdjustment, "xp-adjustment", 0, "experience adjustment (upgraded decks only)")
	fl.BoolVar(&opts.dryRun, "dry-run", false, "show what would be sent without sending it")
	_ = cmd.RegisterFlagCompletionFunc("problem", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return api.ValidProblems, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("aspect", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return api.Aspects, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func newMyDecksDeleteCommand(f *Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>...",
		Aliases: []string{"rm"},
		Short:   "Delete one of your decks",
		Long:    "Delete decks from your account. This cannot be undone.",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			client, err := f.AuthClient(cmd.Context())
			if err != nil {
				return err
			}

			if !yes {
				names := make([]string, 0, len(ids))
				for _, id := range ids {
					label := strconv.Itoa(id)
					if d, err := client.LoadDeck(cmd.Context(), id); err == nil {
						label = fmt.Sprintf("%d (%s)", id, d.Name)
					}
					names = append(names, label)
				}
				ok, err := confirm(cmd.InOrStdin(), cmd.ErrOrStderr(),
					fmt.Sprintf("Permanently delete %s? This cannot be undone.", strings.Join(names, ", ")))
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("cancelled")
				}
			}

			var failures []error
			deleted := make([]int, 0, len(ids))
			for _, id := range ids {
				if _, err := client.DeleteDeck(cmd.Context(), id); err != nil {
					failures = append(failures, err)
					continue
				}
				deleted = append(deleted, id)
			}
			if len(deleted) > 0 {
				t := &output.Table{Headers: []string{"ID", "STATUS"}}
				for _, id := range deleted {
					t.Rows = append(t.Rows, []string{strconv.Itoa(id), "deleted"})
				}
				if err := f.printer.Print(map[string]any{"deleted": deleted}, t); err != nil {
					return err
				}
			}
			return errors.Join(failures...)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newMyDecksPublishCommand(f *Factory) *cobra.Command {
	var name, description, precedent string
	var tournamentID int
	var yes bool

	cmd := &cobra.Command{
		Use:   "publish <id>",
		Short: "Publish a deck as a public decklist",
		Long: strings.TrimSpace(`
Publish one of your decks. The result is a decklist visible to everyone on
MarvelCDB, so the command confirms before it acts.

--precedent is accepted because the API documents it, but MarvelCDB's handler
has a server-side bug on that path: supplying a numeric precedent makes the
request fail with HTTP 500. Leave it unset unless upstream has been fixed.`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			client, err := f.AuthClient(cmd.Context())
			if err != nil {
				return err
			}
			if !yes {
				label := strconv.Itoa(ids[0])
				if d, err := client.LoadDeck(cmd.Context(), ids[0]); err == nil {
					label = fmt.Sprintf("%d (%s)", ids[0], d.Name)
				}
				ok, err := confirm(cmd.InOrStdin(), cmd.ErrOrStderr(),
					fmt.Sprintf("Publish deck %s publicly on MarvelCDB?", label))
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("cancelled")
				}
			}
			res, err := client.PublishDeck(cmd.Context(), ids[0], api.PublishDeckRequest{
				Name: name, Description: description,
				TournamentID: tournamentID, Precedent: precedent,
			})
			if err != nil {
				return err
			}
			return printMutation(f, res, "published decklist")
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&name, "name", "", "name for the published decklist")
	fl.StringVar(&description, "description", "", "decklist description in Markdown")
	fl.IntVar(&tournamentID, "tournament-id", 0, "tournament type id")
	fl.StringVar(&precedent, "precedent", "", "predecessor decklist id (see the note above)")
	fl.BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// resolveMeta reconciles --aspect with --meta, which both write the same field.
func resolveMeta(meta, aspect string) (string, error) {
	if aspect == "" {
		return meta, nil
	}
	if meta != "" {
		return "", errors.New("use either --aspect or --meta, not both")
	}
	blob, err := json.Marshal(map[string]string{"aspect": strings.ToLower(aspect)})
	if err != nil {
		return "", fmt.Errorf("encode meta: %w", err)
	}
	return string(blob), nil
}

// collectSlots merges CODE=QUANTITY pairs with an optional JSON file.
func collectSlots(pairs []string, file string) (api.Slots, error) {
	slots := api.Slots{}

	if file != "" {
		data, err := readInput(file)
		if err != nil {
			return nil, err
		}
		fromFile, err := parseSlotsJSON(data)
		if err != nil {
			return nil, fmt.Errorf("read slots from %s: %w", file, err)
		}
		for code, qty := range fromFile {
			slots[code] = qty
		}
	}

	for _, pair := range pairs {
		code, qtyStr, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("slot %q must be CODE=QUANTITY", pair)
		}
		code = strings.TrimSpace(code)
		qty, err := strconv.Atoi(strings.TrimSpace(qtyStr))
		if err != nil {
			return nil, fmt.Errorf("slot %q has a non-numeric quantity", pair)
		}
		if code == "" {
			return nil, fmt.Errorf("slot %q is missing a card code", pair)
		}
		if qty <= 0 {
			// Zero is how you remove a card from a rebuilt slot list.
			delete(slots, code)
			continue
		}
		slots[code] = qty
	}

	if len(slots) == 0 {
		return nil, nil
	}
	return slots, nil
}

// parseSlotsJSON accepts either a bare {"code": qty} map or a whole deck object.
func parseSlotsJSON(data []byte) (api.Slots, error) {
	var deck struct {
		Slots api.Slots `json:"slots"`
	}
	if err := json.Unmarshal(data, &deck); err == nil && len(deck.Slots) > 0 {
		return deck.Slots, nil
	}
	var slots api.Slots
	if err := json.Unmarshal(data, &slots); err != nil {
		return nil, fmt.Errorf("expected a JSON object of card code to quantity: %w", err)
	}
	return slots, nil
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return data, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path is the user's own argument
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

func totalQty(slots api.Slots) int {
	n := 0
	for _, q := range slots {
		n += q
	}
	return n
}

func printMutation(f *Factory, res api.MutationResult, action string) error {
	msg := res.Message()
	t := &output.Table{
		Headers: []string{"RESULT", "ID"},
		Rows:    [][]string{{action, msg}},
		Text:    fmt.Sprintf("%s: %s\n", action, msg),
	}
	return f.printer.Print(res, t)
}

// confirm asks a yes/no question on stderr, so piped stdout stays clean.
func confirm(in io.Reader, errOut io.Writer, question string) (bool, error) {
	_, _ = fmt.Fprintf(errOut, "%s [y/N]: ", question)
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
