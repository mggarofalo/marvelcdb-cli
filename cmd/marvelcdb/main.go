// Command marvelcdb is a command-line client for MarvelCDB.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/mggarofalo/marvelcdb-cli/internal/cli"
	buildinfo "github.com/mggarofalo/marvelcdb-cli/internal/version"
)

// Injected at link time by the Makefile and the release workflow.
var (
	version = ""
	commit  = ""
)

func main() {
	// os.Exit skips deferred functions, so the real work lives in run().
	os.Exit(run())
}

func run() int {
	buildinfo.Set(version, commit)

	// Ctrl-C cancels in-flight requests instead of leaving them dangling.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return cli.Execute(ctx)
}
