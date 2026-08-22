package cli

import (
	"github.com/spf13/cobra"

	"github.com/mggarofalo/marvelcdb-cli/internal/api"
	"github.com/mggarofalo/marvelcdb-cli/internal/output"
)

func newFactionsCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "factions",
		Aliases: []string{"faction", "aspects"},
		Short:   "List aspects and factions",
	}
	cmd.AddCommand(newFactionsListCommand(f))
	return cmd
}

func newFactionsListCommand(f *Factory) *cobra.Command {
	var primaryOnly bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every faction",
		Long: "List the four aspects, plus the pseudo-factions MarvelCDB uses for hero,\n" +
			"basic, encounter and campaign cards. PRIMARY marks a deckbuilding aspect.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := f.Client()
			if err != nil {
				return err
			}
			factions, err := client.Factions(cmd.Context())
			if err != nil {
				return err
			}
			if primaryOnly {
				kept := make([]api.Faction, 0, len(factions))
				for _, x := range factions {
					if boolValue(x.IsPrimary) {
						kept = append(kept, x)
					}
				}
				factions = kept
			}
			t := &output.Table{
				Headers: []string{"CODE", "NAME", "PRIMARY"},
				Rows:    make([][]string, 0, len(factions)),
			}
			for _, x := range factions {
				t.Rows = append(t.Rows, []string{x.Code, x.Name, yesNo(boolValue(x.IsPrimary))})
			}
			return f.printer.Print(factions, t)
		},
	}
	cmd.Flags().BoolVar(&primaryOnly, "primary", false, "only the deckbuilding aspects")
	return cmd
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
