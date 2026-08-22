package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/mggarofalo/marvelcdb-cli/internal/version"
)

func newVersionCommand() *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if short {
				_, _ = fmt.Fprintln(out, version.Version())
				return nil
			}
			_, _ = fmt.Fprintf(out, "marvelcdb %s\n", version.Version())
			_, _ = fmt.Fprintf(out, "commit:   %s\n", version.Commit())
			_, _ = fmt.Fprintf(out, "go:       %s\n", runtime.Version())
			_, _ = fmt.Fprintf(out, "platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "print only the version number")
	return cmd
}
