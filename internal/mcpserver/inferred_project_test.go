package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
)

// #169 made an unknown folder name fall back to the most recently
// checkpointed project in the whole vault, on the theory that a name the
// vault has never heard of is almost always a scratch directory. It is just
// as often a second real project that has simply not been checkpointed yet,
// and nothing in a folder name tells the two apart — so a host standing in a
// brand-new "snowflake" was handed an unrelated "email" project's checkpoint
// and the mix-up read as snowflake's own history (#326). resume now reports
// the folder unknown, by name, instead of guessing which other project to
// hand over.
func TestResumeInAFolderTheVaultDoesNotKnowNamesItInsteadOfGuessingAnotherProject(t *testing.T) {
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
	if strings.Contains(out, "quote the extruded option") || strings.Contains(out, "rewrite the heron importer") {
		t.Errorf("resume borrowed another project's checkpoint for an unknown folder:\n%s", out)
	}
	// A guess is not a claim on the project. The agent is standing in a
	// different folder and will likely work there; a "resumed the project"
	// note opened a session in kestrel that nobody would ever close.
	if notes, _ := session.Uncommitted(db, "kestrel"); len(notes) > 0 {
		t.Errorf("resume filed a working note into a project it never resumed: %+v", notes)
	}
	if !strings.Contains(out, "Nothing in this vault is filed under") || !strings.Contains(out, "scratchfolder") {
		t.Errorf("resume did not say the folder was unknown:\n%s", out)
	}
	if !strings.Contains(out, "kestrel") || !strings.Contains(out, "heron") {
		t.Errorf("resume did not name the projects the vault does hold:\n%s", out)
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

// A tester's agent asked for "Saathi backend" with the work filed as saathi.
// The name is still honoured — saathi's work is not handed over unasked — but
// the first thing the agent reads is the name to call again with.
func TestResumeOfADescriptiveNameSuggestsTheProjectInsideIt(t *testing.T) {
	t.Setenv("LOGOS_PROJECT", "")
	c, db, vault := startServer(t)
	handshake(t, c)
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	cp := session.Checkpoint{Project: "saathi", Agent: "claude-code", Next: "add auth to the api", TS: time.Now().Unix()}
	if err := session.Commit(db, vault, &cp); err != nil {
		t.Fatal(err)
	}

	out, isErr := c.callText(t, "resume", map[string]any{"project": "Saathi backend"})
	if isErr {
		t.Fatalf("resume errored: %s", out)
	}
	if strings.Contains(out, "add auth to the api") {
		t.Errorf("a name the caller gave was swapped for another project:\n%s", out)
	}
	if !strings.HasPrefix(out, "_Did you mean saathi?") {
		t.Errorf("the project inside the name was not the first thing said:\n%s", out)
	}
}
