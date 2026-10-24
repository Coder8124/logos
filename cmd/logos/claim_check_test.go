package main

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
)

// checkpointOut runs `logos checkpoint` and returns what it printed.
func checkpointOut(t *testing.T, args ...string) string {
	t.Helper()
	return captureStdout(t, func() {
		if err := runCheckpoint(append([]string{"shop"}, args...)); err != nil {
			t.Fatalf("checkpoint: %v", err)
		}
	})
}

// A verified line is the one the next agent builds on, and "go test ./auth
// passes" from a session that only ever ran go build reads exactly like one
// from a session that ran the test. The log saw what ran, so the line it never
// saw is marked — in the file, where resume reads it, not only in the receipt.
func TestAVerifiedCommandTheLogNeverSawIsMarkedUnverified(t *testing.T) {
	standIn(t, "shop")
	hookEvent(t, "PostToolUse", `{"session_id":"s-claim","tool_name":"Bash","tool_input":{"command":"cd /repo && go build ./..."}}`)
	hookEvent(t, "PostToolUse", `{"session_id":"s-claim","tool_name":"Bash","tool_input":{"command":"LOGOS_VAULT=/tmp/v go vet ./auth"}}`)
	// Another session ran the test; this one did not.
	hookEvent(t, "PostToolUse", `{"session_id":"s-other","tool_name":"Bash","tool_input":{"command":"go test ./auth"}}`)
	hookEvent(t, "PostToolUse", `{"session_id":"s-claim","tool_name":"Read","tool_input":{"file_path":"auth.go"}}`)

	out := checkpointOut(t, "--task", "fix token expiry", "--next", "ship it",
		"--verified", "go test ./auth passes",
		"--verified", "go build/vet clean",
		"--verified", "expired tokens are rejected")
	if !strings.Contains(out, "Marked unverified: 1 --verified line") || !strings.Contains(out, "go test ./auth passes") {
		t.Errorf("the receipt does not name the unverified line:\n%s", out)
	}

	c, err := session.Latest(vaultPath(), "shop")
	if err != nil || c == nil {
		t.Fatalf("no checkpoint: %v", err)
	}
	if len(c.Unseen) != 1 || c.Unseen[0] != "go test ./auth passes" {
		t.Errorf("the file marks %q, want only the test the log never saw", c.Unseen)
	}
	if len(c.Verified) != 3 {
		t.Errorf("verified was changed rather than marked: %q", c.Verified)
	}

	resumed := captureStdout(t, func() {
		if err := runResume([]string{"shop"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(resumed, "go test ./auth passes — _unverified") {
		t.Errorf("resume does not mark the line:\n%s", resumed)
	}
	if strings.Contains(resumed, "go build/vet clean — _unverified") || strings.Contains(resumed, "rejected — _unverified") {
		t.Errorf("resume marked a line the log supports or that names no command:\n%s", resumed)
	}
}

// A session whose commands kept failing and whose checkpoint says nothing
// failed hands the next agent a clean record of a messy hour, and the next
// agent walks the same dead ends. The receipt asks, once: a second checkpoint
// in the same session is not nagged again.
func TestAnEmptyFailedOnASessionWithFailingCommandsIsQuestionedOnce(t *testing.T) {
	standIn(t, "shop")
	hookEvent(t, "PostToolUseFailure", `{"session_id":"s-red","tool_name":"Bash","tool_input":{"command":"go test ./cart"}}`)
	hookEvent(t, "PostToolUse", `{"session_id":"s-red","tool_name":"Bash","tool_input":{"command":"go build ./..."}}`)

	first := checkpointOut(t, "--task", "fix the cart", "--next", "ship it")
	if !strings.Contains(first, "--failed is empty, but this session's activity log shows 1 failing tool call") ||
		!strings.Contains(first, "go test ./cart") {
		t.Errorf("an empty --failed after a failing command was not questioned:\n%s", first)
	}
	second := checkpointOut(t, "--task", "fix the cart", "--next", "ship it now")
	if strings.Contains(second, "--failed is empty") {
		t.Errorf("the question was asked twice in one session:\n%s", second)
	}

	// Filled in, there is nothing to ask — in a fresh session too.
	hookEvent(t, "PostToolUseFailure", `{"session_id":"s-red2","tool_name":"Bash","tool_input":{"command":"go test ./cart"}}`)
	filled := checkpointOut(t, "--task", "fix the cart", "--next", "ship it", "--failed", "mocking the clock: the race is in the cache")
	if strings.Contains(filled, "is empty, but") {
		t.Errorf("a filled --failed was questioned:\n%s", filled)
	}
}

// A vault that never kept a log cannot say a command did not run, so nothing
// is marked and nothing is asked.
func TestWithNoActivityLogNoClaimIsMarked(t *testing.T) {
	standIn(t, "shop")
	out := checkpointOut(t, "--task", "fix token expiry", "--next", "ship it", "--verified", "go test ./auth passes")
	if strings.Contains(out, "unverified") || strings.Contains(out, "activity log") {
		t.Errorf("a vault with no log judged a claim:\n%s", out)
	}
	c, _ := session.Latest(vaultPath(), "shop")
	if c == nil || len(c.Unseen) != 0 {
		t.Errorf("marked with no log to go on: %+v", c)
	}
}
