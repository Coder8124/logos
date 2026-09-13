package main

import (
	"os"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

// `logos review --help` started the interactive review: with anything pending
// it printed the first memory and waited on stdin, so an agent asking for the
// flags hung its shell call until the tool timed out.
func TestReviewHelpPrintsTheFlagsInsteadOfStartingTheReview(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		setupInFakeHome(t)
		var err error
		out := captureStdout(t, func() { err = runReview([]string{flag}) })
		if err != nil {
			t.Fatalf("review %s: %v", flag, err)
		}
		if !strings.Contains(out, "logos review [--all]") {
			t.Errorf("review %s did not print its usage:\n%s", flag, out)
		}
	}
}

// Accepting a memory writes it to the database and the vault at once, so it
// is recallable straight away. Ending the review with "run `logos index` to
// pick up the new notes" sent people to a command that changed nothing.
func TestAcceptingAMemoryInReviewDoesNotSendYouToLogosIndex(t *testing.T) {
	dir := setupInFakeHome(t)
	ix, err := openEventsAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	m := memory.Memory{Text: "invoices go out on the first", Kind: memory.Fact, Source: "mcp", Quarantined: true}
	if _, err := memory.Store(ix.DB, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.WriteString("a\n")
	w.Close()
	stdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = stdin })

	out := captureStdout(t, func() { err = runReview(nil) })
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !strings.Contains(out, "1 accepted") {
		t.Fatalf("the memory was not accepted:\n%s", out)
	}
	if strings.Contains(out, "logos index") {
		t.Errorf("review still tells you to run logos index:\n%s", out)
	}
}
