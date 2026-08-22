package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/mggarofalo/marvelcdb-cli/internal/api"
	"github.com/mggarofalo/marvelcdb-cli/internal/output"
)

// completionTimeout keeps a shell completion from hanging on a slow network.
const completionTimeout = 3 * time.Second

// ensureResolved initialises the factory if the completion machinery reached us
// without running PersistentPreRunE, which is how cobra dispatches __complete.
func (f *Factory) ensureResolved(cmd *cobra.Command) error {
	if f.printer != nil {
		return nil
	}
	return f.resolve(flagsChangedOn(cmd))
}

// completionContext bounds completion lookups so a slow network never leaves a
// shell hanging. Callers defer the returned cancel.
func completionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, completionTimeout) //nolint:gosec // G118: cancel is returned and deferred by every caller
}

func (f *Factory) completeCards(cmd *cobra.Command) []api.Card {
	if err := f.ensureResolved(cmd); err != nil {
		return nil
	}
	client, err := f.Client()
	if err != nil {
		return nil
	}
	cctx, cancel := completionContext(cmd.Context())
	defer cancel()
	cards, err := client.Cards(cctx, true)
	if err != nil {
		return nil
	}
	return cards
}

func (f *Factory) completePacks(cmd *cobra.Command) []string {
	if err := f.ensureResolved(cmd); err != nil {
		return nil
	}
	client, err := f.Client()
	if err != nil {
		return nil
	}
	cctx, cancel := completionContext(cmd.Context())
	defer cancel()
	packs, err := client.Packs(cctx)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(packs))
	for _, p := range packs {
		out = append(out, p.Code+"\t"+p.Name)
	}
	return out
}

func (f *Factory) completeFactions(cmd *cobra.Command) []string {
	if err := f.ensureResolved(cmd); err != nil {
		return nil
	}
	client, err := f.Client()
	if err != nil {
		return nil
	}
	cctx, cancel := completionContext(cmd.Context())
	defer cancel()
	factions, err := client.Factions(cctx)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(factions))
	for _, x := range factions {
		out = append(out, x.Code+"\t"+x.Name)
	}
	return out
}

// registerCardCompletions attaches dynamic completions to whichever of the
// shared card flags a command actually defines.
func registerCardCompletions(f *Factory, cmd *cobra.Command) {
	register := func(flag string, fn func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective)) {
		if cmd.Flags().Lookup(flag) == nil {
			return
		}
		_ = cmd.RegisterFlagCompletionFunc(flag, fn)
	}

	register("pack", func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return f.completePacks(cmd), cobra.ShellCompDirectiveNoFileComp
	})
	register("faction", func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return f.completeFactions(cmd), cobra.ShellCompDirectiveNoFileComp
	})
	register("type", func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return uniqueField(f.completeCards(cmd), func(c api.Card) (string, string) {
			return c.TypeCode, c.TypeName
		}), cobra.ShellCompDirectiveNoFileComp
	})
	register("set", func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return uniqueField(f.completeCards(cmd), func(c api.Card) (string, string) {
			return c.CardSetCode, c.CardSetName
		}), cobra.ShellCompDirectiveNoFileComp
	})
	register("sort", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return sortFields, cobra.ShellCompDirectiveNoFileComp
	})

	if cmd.ValidArgsFunction == nil && usesCodeArg(cmd.Use) {
		cmd.ValidArgsFunction = func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			cards := f.completeCards(cmd)
			out := make([]string, 0, 64)
			for _, c := range cards {
				if len(out) >= 200 {
					break
				}
				if toComplete == "" || hasPrefixFold(c.Code, toComplete) || containsFold(c.Name, toComplete) {
					out = append(out, c.Code+"\t"+c.Name)
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		}
	}
}

// registerGlobalCompletions attaches completions to the root's shared flags.
func registerGlobalCompletions(cmd *cobra.Command) {
	_ = cmd.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return output.Formats(), cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("locale", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return api.Locales, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("color", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"auto", "always", "never"}, cobra.ShellCompDirectiveNoFileComp
	})
}

func uniqueField(cards []api.Card, pick func(api.Card) (string, string)) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 32)
	for _, c := range cards {
		code, label := pick(c)
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code+"\t"+label)
	}
	return out
}

func hasPrefixFold(s, prefix string) bool {
	if len(prefix) > len(s) {
		return false
	}
	return containsFold(s[:len(prefix)], prefix)
}

// usesCodeArg reports whether a command takes card codes as positional args.
func usesCodeArg(use string) bool { return containsFold(use, "<code>") }
