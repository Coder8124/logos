package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
)

// #169: the fallback to the most recent checkpoint only fired in /, the one
// folder with no basename. From any real folder the vault had never heard of,
// resume said "Nothing recorded" while two projects sat one call away.
func TestResumeInAFolderTheVaultDoesNotKnowPicksUpTheMostRecentCheckpoint(t *testing.T) {
	t.Setenv("LOGOS_PROJECT", "")
	here := filepath.Join(t.TempDir(), "scratchfolder")
	if err := os.Mkdir(here, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(here)
	c, db, vault := startServer(t)
	handshake(t, c)
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	for _, cp := range []session.Checkpoint{
		{Project: "heron", Agent: "cursor", Next: "rewrite the heron importer", TS: time.Now().Add(-48 * time.Hour).Unix()},
		{Project: "kestrel", Agent: "claude-code", Next: "quote the extruded option", TS: time.Now().Add(-time.Hour).Unix()},
	} {
		if err := session.Commit(db, vault, &cp); err != nil {
			t.Fatal(err)
		}
	}

	out, isErr := c.callText(t, "resume", map[string]any{"agent": "codex"})
	if isErr {
		t.Fatalf("resume errored: %s", out)
	}
	// A guess is not a claim on the project. The agent is standing in a
	// different folder and will likely work there; a "resumed the project"
	// note opened a session in kestrel that nobody would ever close.
	if notes, _ := session.Uncommitted(db, "kestrel"); len(notes) > 0 {
		t.Errorf("resume filed a working note into the project it only guessed at: %+v", notes)
	}
	if !strings.Contains(out, "quote the extruded option") {
		t.Errorf("resume did not hand over the most recent checkpoint:\n%s", out)
	}
	if !strings.Contains(out, "scratchfolder") || !strings.Contains(out, "resuming kestrel") {
		t.Errorf("resume did not say the folder was unknown and which project it chose:\n%s", out)
	}
}

// The other half of #169: a name the caller typed is honoured, not swapped for
// another project's work — but the dead end names what the vault does hold.
func TestResumeOfAGivenNameTheVaultDoesNotKnowNamesTheProjectsItDoes(t *testing.T) {
	t.Setenv("LOGOS_PROJECT", "")
	c, db, vault := startServer(t)
	handshake(t, c)
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	cp := session.Checkpoint{Project: "kestrel", Agent: "claude-code", Next: "quote the extruded option", TS: time.Now().Unix()}
	if err := session.Commit(db, vault, &cp); err != nil {
		t.Fatal(err)
	}

	out, isErr := c.callText(t, "resume", map[string]any{"project": "kestral"})
	if isErr {
		t.Fatalf("resume errored: %s", out)
	}
	if strings.Contains(out, "quote the extruded option") {
		t.Errorf("a name the caller gave was swapped for another project:\n%s", out)
	}
	if !strings.Contains(out, "Known projects: kestrel") {
		t.Errorf("the dead end did not name the project the vault holds:\n%s", out)
	}
}
