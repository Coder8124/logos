package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

// The file header invites editing a line. A line retyped whole drops its
// `<!-- logos id=… -->` comment, and without it the index cannot tell the edit
// from a new fact: the old memory is forgotten, project and confidence with it,
// and a new one takes its place. That is the right reading of the file, but
// `logos index` said "+0 ~0 -0" over it, and only `logos memory log` knew.
func TestALineRetypedWithoutItsCommentIsAnnouncedAsReplacingItsMemory(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_EMBED", "off")

	if err := memoryCmd([]string{"add", "--project", "shop", "Decided to use Stripe for payments"}); err != nil {
		t.Fatalf("memory add: %v", err)
	}
	if err := runIndex(false); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(vaultDir, "memories", "fact.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	retyped := regexp.MustCompile(`(?m)^- Decided to use Stripe for payments.*$`).
		ReplaceAllString(string(raw), "- Decided to use Stripe for payments, not PayPal")
	if retyped == string(raw) {
		t.Fatalf("the memory line was not found in:\n%s", raw)
	}
	if err := os.WriteFile(path, []byte(retyped), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runIndex(false); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"#2", "#1", "logos id="} {
		if !strings.Contains(out, want) {
			t.Errorf("index did not say %q about the memory it replaced:\n%s", want, out)
		}
	}

	// Said once, for the run that did it, not on every index after.
	again := captureStdout(t, func() {
		if err := runIndex(false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(again, "logos id=") {
		t.Errorf("a later index repeated the replacement:\n%s", again)
	}
}

// A line index could not make a memory is named on every run it is still
// there, since the next write to its file drops it.
func TestALineLeftOutOfMemoryIsNamedByIndex(t *testing.T) {
	var b bytes.Buffer
	printMemoryEdits(&b, memory.Edits{Held: []string{"a line in memories/fact.md is the text of memory #7, which is awaiting review"}})
	if !strings.Contains(b.String(), "left out of memory") || !strings.Contains(b.String(), "#7") {
		t.Errorf("index did not name the line it left out:\n%s", b.String())
	}
}
