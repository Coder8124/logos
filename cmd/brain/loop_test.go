package main

import (
	"strings"
	"testing"
)

// `brain loop` with no arguments is the list, and nothing says so. Every other
// listing verb in this CLI takes `list`, the help line reads "brain loop
// [add|done|drop]" — which names three verbs, none of them the one that shows
// you your commitments — and `brain loop list` answered with a usage error.
// Someone with minutes to spare reads that error as "there is no way to see
// them" and stops.
func TestLoopListIsTheListRatherThanAUsageError(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("BRAIN_VAULT", vaultDir)
	t.Setenv("BRAIN_EMBED", "off")

	const text = "send the optics quote back to the vendor"
	if err := commitmentCmd([]string{"add", text}); err != nil {
		t.Fatalf("loop add: %v", err)
	}

	out := captureStdout(t, func() {
		if err := commitmentCmd([]string{"list"}); err != nil {
			t.Fatalf("loop list: %v", err)
		}
	})
	if !strings.Contains(out, text) {
		t.Errorf("`brain loop list` did not list the loop:\n%s", out)
	}

	bare := captureStdout(t, func() {
		if err := commitmentCmd(nil); err != nil {
			t.Fatal(err)
		}
	})
	if bare != out {
		t.Errorf("`brain loop list` and `brain loop` disagree:\n%q\nvs\n%q", out, bare)
	}
}

// Closing a loop that does not exist reported success. The UPDATE matched no
// row, SetStatus returned nil, and the CLI printed nothing — so a mistyped id
// was indistinguishable from a loop closed, and the loop the user meant to
// close stayed open with nothing to say it had not been.
func TestClosingALoopThatDoesNotExistSaysSo(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("BRAIN_VAULT", vaultDir)
	t.Setenv("BRAIN_EMBED", "off")

	for _, verb := range []string{"done", "drop"} {
		if err := commitmentCmd([]string{verb, "99"}); err == nil {
			t.Errorf("`brain loop %s 99` reported success for a loop that does not exist", verb)
		}
		if err := commitmentCmd([]string{verb}); err == nil {
			t.Errorf("`brain loop %s` with no id reported success", verb)
		}
	}
}
