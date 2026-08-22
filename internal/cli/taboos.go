package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/mggarofalo/marvelcdb-cli/internal/output"
)

func newTaboosCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "taboos",
		Aliases: []string{"taboo"},
		Short:   "Show the banned and restricted list",
	}
	cmd.AddCommand(newTaboosListCommand(f))
	return cmd
}

func newTaboosListCommand(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List taboo entries",
		Long: strings.TrimSpace(`
List MarvelCDB's banned and restricted entries.

Heads-up: this endpoint is routed on MarvelCDB but currently fails server-side,
returning HTTP 500. The command is here for when upstream fixes it; until then
expect an error rather than an empty list.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := f.Client()
			if err != nil {
				return err
			}
			taboos, err := client.Taboos(cmd.Context())
			if err != nil {
				return err
			}
			t := &output.Table{
				Headers: []string{"ID", "CODE", "NAME", "STARTS"},
				Rows:    make([][]string, 0, len(taboos)),
			}
			for _, x := range taboos {
				t.Rows = append(t.Rows, []string{optionalInt(x.ID), x.Code, x.Name, x.Date})
			}
			return f.printer.Print(taboos, t)
		},
	}
}
