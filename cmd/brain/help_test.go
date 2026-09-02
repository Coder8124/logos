package main

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The short help earns its place by being short. A screen is the whole point of
// the split — the moment someone appends "just one more useful verb" it starts
// sliding back into the inventory it replaced, and nobody notices, because help
// text has no failing state.
func TestHelpShortFitsAScreen(t *testing.T) {
	var buf bytes.Buffer
	helpShort(&buf)

	const maxLines = 40
	if n := strings.Count(buf.String(), "\n"); n > maxLines {
		t.Errorf("short help is %d lines, want at most %d — move the new verb to helpAll", n, maxLines)
	}
	// The rest of the surface is only discoverable if the short form says so.
	if !strings.Contains(buf.String(), "brain help all") {
		t.Error("short help does not mention `brain help all`, so the other verbs are unreachable")
	}
}

// Demoting the general surface must not amount to losing it. Every verb main
// dispatches on has to appear in `brain help all`, so a command added later
// cannot end up working and undocumented — the failure mode of a help text that
// is no longer the same list as the code.
func TestHelpAllListsEveryVerb(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	helpAll(&buf)
	all := buf.String()

	// The dispatch is a switch of `cmd == "verb"` comparisons, so the source is
	// the list. Reading it beats maintaining a second copy here that drifts.
	verbs := regexp.MustCompile(`cmd == "([a-z]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(verbs) < 30 {
		t.Fatalf("found only %d verbs in main.go — the dispatch shape changed and this test no longer reads it", len(verbs))
	}

	for _, m := range verbs {
		verb := m[1]
		// Word-boundary rather than "brain <verb>": several verbs are documented
		// on a shared line, as `brain voice | listen | say <text>`.
		if !regexp.MustCompile(`\b` + verb + `\b`).MatchString(all) {
			t.Errorf("verb %q is dispatched by main but absent from `brain help all`", verb)
		}
	}
}
