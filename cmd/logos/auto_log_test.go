package main

import (
	"os"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/session"
)

// A checkpoint written at session end happened with nobody watching: the hook
// printed its line into a closing terminal. The next resume is the first place
// anybody can be told, so it says so, with the number.
func TestResumeSaysWhatAutoDidSinceTheLastSession(t *testing.T) {
	standIn(t, "elsewhere")
	if err := runCheckpoint([]string{"shop", "--task", "fix the cache", "--next", "ship it"}); err != nil {
		t.Fatal(err)
	}
	hookEvent(t, "PostToolUse", `{"session_id":"s-later","tool_name":"Edit","tool_input":{"file_path":"cart.go"}}`)
	if out := endSession(t, "s-later"); !strings.HasPrefix(out, "auto-checkpointed") {
		t.Fatalf("no auto checkpoint to report: %q", out)
	}
	if _, err := os.Stat(auto.LogPath(vaultPath())); err != nil {
		t.Fatalf("the session-end checkpoint left nothing in %s: %v", auto.LogFile, err)
	}

	resumed := captureStdout(t, func() {
		if err := runResume([]string{"shop"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(resumed, "auto: checkpointed 1 session") {
		t.Errorf("resume did not say what auto did since the last session:\n%s", resumed)
	}
}

// The switch is the user's answer to "may Logos write a checkpoint I did not
// ask for"; a session end under `checkpoint: off` leaves the plain note.
func TestTheCheckpointSwitchOffStopsTheSessionEndCheckpoint(t *testing.T) {
	standIn(t, "elsewhere")
	if err := auto.Set(vaultPath(), "checkpoint", false); err != nil {
		t.Fatal(err)
	}
	hookEvent(t, "PostToolUse", `{"session_id":"s-off","tool_name":"Edit","tool_input":{"file_path":"cart.go"}}`)

	if out := endSession(t, "s-off"); strings.HasPrefix(out, "auto-checkpointed") {
		t.Errorf("a session end wrote a checkpoint with the switch off: %q", out)
	}
	if c, _ := session.Latest(vaultPath(), "shop"); c != nil {
		t.Errorf("a checkpoint was written with the switch off: %+v", c)
	}
}

// The edit hook is automatic too: with rulings-at-edit off, editing the file
// a ruling names shows nothing.
func TestTheRulingsAtEditSwitchOffSilencesTheEditHook(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, readerRuling)
	if err := auto.Set(vault, "rulings-at-edit", false); err != nil {
		t.Fatal(err)
	}
	if got := edit(t, vault, repo, "s1", "internal/parse/reader.go"); got != "" {
		t.Errorf("the edit hook showed a ruling with its switch off:\n%s", got)
	}
}
