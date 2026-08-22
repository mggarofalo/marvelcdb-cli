package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mggarofalo/marvelcdb-cli/internal/config"
	"github.com/mggarofalo/marvelcdb-cli/internal/output"
)

func newConfigCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and write the config file",
		Long: strings.TrimSpace(`
Read and write persistent settings.

The config file holds preferences only. Credentials live in the OS keyring, so
this file is safe to read, diff and share.

Precedence, lowest to highest: built-in defaults, config file, environment
variables, command-line flags.`),
	}
	cmd.AddCommand(
		newConfigListCommand(f),
		newConfigGetCommand(f),
		newConfigSetCommand(f),
		newConfigPathCommand(f),
		newConfigInitCommand(f),
	)
	return cmd
}

func newConfigListCommand(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"view", "show"},
		Short:   "Show the effective settings",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			t := &output.Table{Headers: []string{"SETTING", "VALUE"}}
			for _, key := range config.Fields() {
				v, err := f.cfg.Get(key)
				if err != nil {
					return err
				}
				t.Rows = append(t.Rows, []string{key, v})
			}
			return f.printer.Print(f.cfg, t)
		},
	}
}

func newConfigGetCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <setting>",
		Short: "Print one setting",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := f.cfg.Get(args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), v)
			return nil
		},
		ValidArgsFunction: completeConfigFields,
	}
	return cmd
}

func newConfigSetCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "set <setting> <value>",
		Short:   "Write one setting to the config file",
		Example: "  marvelcdb config set output json\n  marvelcdb config set cache_ttl 24h",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Re-read the file so flags used on this invocation are not baked in.
			stored, err := config.Load(f.flags.configPath)
			if err != nil {
				return err
			}
			if err := stored.Set(args[0], args[1]); err != nil {
				return err
			}
			if err := stored.Save(f.flags.configPath); err != nil {
				return err
			}
			if !f.flags.quiet {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Set %s to %s in %s\n", args[0], args[1], f.flags.configPath)
			}
			return nil
		},
		ValidArgsFunction: completeConfigFields,
	}
	return cmd
}

func newConfigPathCommand(f *Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the config file path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), f.flags.configPath)
			return nil
		},
	}
}

func newConfigInitCommand(f *Factory) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a config file with the current settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := os.Stat(f.flags.configPath); err == nil && !force {
				return fmt.Errorf("%s already exists; pass --force to overwrite it", f.flags.configPath)
			}
			if err := f.cfg.Save(f.flags.configPath); err != nil {
				return err
			}
			if !f.flags.quiet {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %s\n", f.flags.configPath)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config file")
	return cmd
}

func completeConfigFields(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return config.Fields(), cobra.ShellCompDirectiveNoFileComp
}
