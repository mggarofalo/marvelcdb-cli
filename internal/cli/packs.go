package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mggarofalo/marvelcdb-cli/internal/api"
	"github.com/mggarofalo/marvelcdb-cli/internal/output"
)

func newPacksCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "packs",
		Aliases: []string{"pack", "sets"},
		Short:   "List MarvelCDB packs",
	}
	cmd.AddCommand(newPacksListCommand(f), newPacksGetCommand(f))
	return cmd
}

func newPacksListCommand(f *Factory) *cobra.Command {
	var sortBy string
	var reverse bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every pack",
		Long: "List every product MarvelCDB knows about. KNOWN is how many cards have\n" +
			"been entered; TOTAL is how many the pack contains.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := f.Client()
			if err != nil {
				return err
			}
			packs, err := client.Packs(cmd.Context())
			if err != nil {
				return err
			}
			if err := sortPacks(packs, sortBy, reverse); err != nil {
				return err
			}
			return f.printer.Print(packs, packTable(packs))
		},
	}
	cmd.Flags().StringVar(&sortBy, "sort", "position", "sort by: position, code, name, released")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "reverse the sort order")
	_ = cmd.RegisterFlagCompletionFunc("sort", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"position", "code", "name", "released"}, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func newPacksGetCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "get <pack-code>...",
		Aliases: []string{"show"},
		Short:   "Show one or more packs by code",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.Client()
			if err != nil {
				return err
			}
			all, err := client.Packs(cmd.Context())
			if err != nil {
				return err
			}
			byCode := make(map[string]api.Pack, len(all))
			for _, p := range all {
				byCode[strings.ToLower(p.Code)] = p
			}

			matched := make([]api.Pack, 0, len(args))
			var missing []string
			for _, code := range args {
				p, ok := byCode[strings.ToLower(code)]
				if !ok {
					missing = append(missing, code)
					continue
				}
				matched = append(matched, p)
			}
			if len(missing) > 0 && !f.flags.quiet {
				f.printer.Warn("no such pack: %s", strings.Join(missing, ", "))
			}
			if len(matched) == 0 {
				return api.ErrNotFound
			}
			var payload any = matched
			if len(matched) == 1 {
				payload = matched[0]
			}
			return f.printer.Print(payload, packTable(matched))
		},
	}
	_ = cmd.RegisterFlagCompletionFunc("output", nil)
	cmd.ValidArgsFunction = func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return f.completePacks(cmd), cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

func sortPacks(packs []api.Pack, field string, reverse bool) error {
	var less func(a, b api.Pack) bool
	switch strings.ToLower(field) {
	case "", "position":
		less = func(a, b api.Pack) bool { return intValue(a.Position) < intValue(b.Position) }
	case "code":
		less = func(a, b api.Pack) bool { return a.Code < b.Code }
	case "name":
		less = func(a, b api.Pack) bool { return strings.ToLower(a.Name) < strings.ToLower(b.Name) }
	case "released", "available", "date":
		less = func(a, b api.Pack) bool { return a.Available < b.Available }
	default:
		return errUnknownSort(field, "position, code, name, released")
	}
	sort.SliceStable(packs, func(i, j int) bool {
		if reverse {
			return less(packs[j], packs[i])
		}
		return less(packs[i], packs[j])
	})
	return nil
}

func packTable(packs []api.Pack) *output.Table {
	t := &output.Table{
		Headers: []string{"CODE", "NAME", "RELEASED", "KNOWN", "TOTAL", "URL"},
		Rows:    make([][]string, 0, len(packs)),
	}
	for _, p := range packs {
		t.Rows = append(t.Rows, []string{
			p.Code, p.Name, p.Available,
			optionalInt(p.Known), optionalInt(p.Total), p.URL,
		})
	}
	return t
}
