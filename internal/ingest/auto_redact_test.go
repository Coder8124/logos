package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

const leakedToken = "ghp_R2d2C3poBb8Ee9Ff0Gg1Hh2Ii3Jj4Kk5Ll6M"

// leaky is a session that pasted a token into its first prompt and a shell
// line, and ended without a checkpoint.
func leaky() *transcript.Session {
	return &transcript.Session{
		Harness: "cursor", ID: "sess-9", Path: "/tmp/sess-9.json", Project: "shop",
		Turns: []transcript.Turn{
			{Role: "user", Text: "upload the release with GITHUB_TOKEN=" + leakedToken},
			{Role: "tool", Tool: "run_terminal_cmd", Status: "ok",
				Input: `curl -H "Authorization: Bearer ` + leakedToken + `" https://api.github.com/user`},
		},
	}
}

// An auto record is written with nobody looking: the first prompt becomes the
// task and the title, the shell lines become the commands. No agent chose to
// write any of it, so no agent is there to notice a token went into the vault
// (#209).
func TestAnAutoRecordDoesNotWriteATokenFromTheTranscript(t *testing.T) {
	vault := t.TempDir()
	c, _, err := AutoCheckpoint(vault, leaky(), "shop")
	if err != nil || c == nil {
		t.Fatalf("recorded %v, %v", c, err)
	}
	filepath.Walk(vault, func(p string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), leakedToken) {
				t.Errorf("the token reached the vault in %s", p)
			}
		}
		return nil
	})
	if len(c.Redactions) == 0 {
		t.Error("the record was masked without saying so")
	}
	if !strings.Contains(SweepNotice([]session.Checkpoint{*c}, nil, nil), "redacted") {
		t.Error("the resume notice for this record does not say anything was redacted")
	}
}

// The sweep rebuilds a record from its transcript to ask whether the session
// did more since. Masked on one side and not the other, the two never match,
// and the record would be rewritten on every resume.
func TestAnAutoRecordWithAMaskedTokenIsNotRewrittenByEverySweep(t *testing.T) {
	vault := t.TempDir()
	s := leaky()
	if c, _, err := AutoCheckpoint(vault, s, "shop"); err != nil || c == nil {
		t.Fatalf("recorded %v, %v", c, err)
	}
	history, err := session.History(vault, "shop", 0)
	if err != nil || len(history) == 0 {
		t.Fatalf("history %v, %v", history, err)
	}
	if grown, err := growRecord(vault, history, history[0], s, s.Ended); err != nil || grown != nil {
		t.Errorf("an unchanged session was grown: %v, %v", grown, err)
	}
}
