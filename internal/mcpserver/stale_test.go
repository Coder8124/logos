package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
)

// A host keeps the server it started, binary and all, until the host itself
// restarts. After `brew upgrade` every resume and checkpoint still ran 0.5.1
// while 0.5.6 sat installed, and the user asked for a feature they already had.
func TestResumeAndCheckpointSayTheServerIsOlderThanTheInstalledBinary(t *testing.T) {
	t.Setenv("LOGOS_TRUST_MCP", "1")
	t.Setenv("LOGOS_PROJECT", "acme-api")
	db := testDB(t)
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	s := &Session{Server: &Server{DB: db, vault: t.TempDir()}}

	exe := filepath.Join(t.TempDir(), "logos")
	if err := os.WriteFile(exe, []byte("0.5.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	was := started
	t.Cleanup(func() { started = was })
	started = stampOf(exe)

	out, err := s.resume("", "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "has been replaced") {
		t.Errorf("an unchanged binary was reported as replaced:\n%s", out)
	}

	if err := os.WriteFile(exe, []byte("0.5.6, a newer and larger build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err = s.resume("", "", 0, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "has been replaced") || !strings.Contains(out, exe) {
		t.Errorf("resume did not say the server is older than the installed binary:\n%s", out)
	}
	got, err := s.checkpoint(map[string]any{"task": "rename the route", "next": "ship it"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "has been replaced") {
		t.Errorf("the checkpoint receipt did not say so:\n%s", got)
	}
}
