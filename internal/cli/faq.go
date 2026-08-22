package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mggarofalo/marvelcdb-cli/internal/api"
	"github.com/mggarofalo/marvelcdb-cli/internal/output"
)

func newFaqCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "faq <code>...",
		Short: "Show rules clarifications for a card",
		Long: strings.TrimSpace(`
Show the FAQ entries MarvelCDB has recorded against a card.

Entries are HTML on the site; the table and text formats render them as plain
text, while --output json returns them untouched.`),
		Example: "  marvelcdb faq 01001a\n  marvelcdb faq 01001a -o json",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.Client()
			if err != nil {
				return err
			}
			var all []api.FaqEntry
			var failures []error
			for _, code := range args {
				entries, err := client.Faq(cmd.Context(), code)
				if err != nil {
					failures = append(failures, err)
					continue
				}
				if len(entries) == 0 && !f.flags.quiet {
					f.printer.Warn("no FAQ entries for %s", code)
				}
				all = append(all, entries...)
			}
			if len(all) > 0 {
				if err := f.printer.Print(all, faqTable(all)); err != nil {
					return err
				}
			}
			return errors.Join(failures...)
		},
	}
	registerCardCompletions(f, cmd)
	return cmd
}

func faqTable(entries []api.FaqEntry) *output.Table {
	t := &output.Table{
		Headers: []string{"CODE", "ENTRY"},
		Rows:    make([][]string, 0, len(entries)),
	}
	var full strings.Builder
	for i, e := range entries {
		text := output.StripHTML(e.HTML)
		if text == "" {
			text = output.StripHTML(e.Text)
		}
		t.Rows = append(t.Rows, []string{e.Code, output.Truncate(output.Flatten(text), 100)})
		if i > 0 {
			full.WriteString("\n")
		}
		fmt.Fprintf(&full, "[%s]\n%s\n", e.Code, text)
	}
	t.Text = full.String()
	return t
}
