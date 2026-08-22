package version

import (
	"strings"
	"testing"
)

func TestSetAndReport(t *testing.T) {
	Set("v1.2.3", "abc1234")

	if got := Version(); got != "v1.2.3" {
		t.Errorf("Version = %q", got)
	}
	if got := Commit(); got != "abc1234" {
		t.Errorf("Commit = %q", got)
	}
	for _, want := range []string{"marvelcdb", "v1.2.3", "abc1234"} {
		if !strings.Contains(String(), want) {
			t.Errorf("String() = %q, want it to mention %q", String(), want)
		}
	}
	if !strings.Contains(UserAgent(), "v1.2.3") {
		t.Errorf("UserAgent = %q", UserAgent())
	}
	// A User-Agent should say who to contact about the traffic.
	if !strings.Contains(UserAgent(), "github.com/mggarofalo/marvelcdb-cli") {
		t.Errorf("UserAgent = %q, want a contact URL", UserAgent())
	}
}

func TestSetIgnoresEmptyValues(t *testing.T) {
	Set("v9.9.9", "deadbee")
	Set("", "")
	if Version() != "v9.9.9" || Commit() != "deadbee" {
		t.Errorf("empty values overwrote %q/%q", Version(), Commit())
	}
}
