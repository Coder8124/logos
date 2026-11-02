package main

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/bootstrap"
	"github.com/Coder8124/logos/internal/memory"
)

// Inside setup the write's report printed at column 0 between setup's
// two-space rows (#247); every line it prints carries the indent it is given.
func TestTheBootstrapWriteReportIsIndentedInsideSetup(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_RUNTIME", "off")
	seeds := []bootstrap.Candidate{{Text: "Ada writes most of this.", Kind: memory.Context, Confidence: 0.5, Salience: 0.4}}

	out := captureStdout(t, func() {
		if err := storeSeeds(seeds, "shop", "  "); err != nil {
			t.Error(err)
		}
	})
	for _, line := range strings.Split(out, "\n") {
		if line != "" && !strings.HasPrefix(line, "  ") {
			t.Errorf("line at column 0 inside setup: %q\nfull output:\n%s", line, out)
		}
	}
	if !strings.Contains(out, "1 written") {
		t.Fatalf("the write was not reported:\n%s", out)
	}
}
