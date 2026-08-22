// Package version carries build identity, stamped in at link time.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	version = "dev"
	commit  = "unknown"
)

// Set records the values injected by the linker.
func Set(v, c string) {
	if v != "" {
		version = v
	}
	if c != "" {
		commit = c
	}
	if version == "dev" || commit == "unknown" {
		fillFromBuildInfo()
	}
}

// fillFromBuildInfo recovers version data from a `go install`-built binary,
// which carries no ldflags.
func fillFromBuildInfo() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	if commit != "unknown" {
		return
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			if len(s.Value) > 7 {
				commit = s.Value[:7]
			} else {
				commit = s.Value
			}
			return
		}
	}
}

// Version reports the release version.
func Version() string { return version }

// Commit reports the source revision.
func Commit() string { return commit }

// String is the one-line build identity.
func String() string {
	return fmt.Sprintf("marvelcdb %s (commit %s, %s, %s/%s)",
		version, commit, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// UserAgent identifies this build to MarvelCDB.
func UserAgent() string {
	return fmt.Sprintf("marvelcdb-cli/%s (+https://github.com/mggarofalo/marvelcdb-cli)", version)
}
