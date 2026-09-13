package main

import (
	"strings"
	"testing"
)

// `brain review --help` started the interactive review: with anything pending
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
		if !strings.Contains(out, "brain review [--all]") {
			t.Errorf("review %s did not print its usage:\n%s", flag, out)
		}
	}
}
