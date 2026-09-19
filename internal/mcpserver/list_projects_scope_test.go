package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
)

func seedScopeCheckpoint(t *testing.T, vault, scope string) {
	t.Helper()
	dir := filepath.Join(vault, session.CheckpointDir, filepath.FromSlash(scope))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "---\ntype: checkpoint\nproject: " + scope + "\nagent: claude\n---\n\n## Task\n\nwork\n\n## Next\n\nmore work\n"
	if err := os.WriteFile(filepath.Join(dir, "20260919-120000-claude.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// list_projects was the only line in the tool switch that threaded no session
// state, so the one tool whose answer is a list of names could not mark the
// name belonging to the agent asking. An agent handed four names fans out and
// calls resume once per name; three of those answers are somebody else's work.
func TestListProjectsNamesTheProjectTheSessionIsStandingIn(t *testing.T) {
	vault := t.TempDir()
	seedScopeCheckpoint(t, vault, "kestrel")
	seedScopeCheckpoint(t, vault, "heron")
	sess := &Session{Server: &Server{DB: testDB(t), vault: vault}, roots: []string{"/work/kestrel"}}

	out, err := sess.listProjectsHere()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "You are in kestrel") {
		t.Errorf("the list never says which project the caller is in:\n%s", out)
	}
	if strings.Index(out, "kestrel") > strings.Index(out, "heron") {
		t.Errorf("another project is named before the caller's own:\n%s", out)
	}
}

// The resource surface is a directory of the vault rather than advice to an
// agent standing somewhere, so it keeps the unscoped listing — and must not
// grow a "you are in" line that would be false for whoever reads it.
func TestTheProjectsResourceStaysAnUnscopedInventory(t *testing.T) {
	vault := t.TempDir()
	seedScopeCheckpoint(t, vault, "kestrel")
	sess := &Session{Server: &Server{DB: testDB(t), vault: vault}, roots: []string{"/work/kestrel"}}

	out, err := sess.readResource("logos://projects")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "You are in") {
		t.Errorf("the vault's directory listing addresses a caller it does not have:\n%s", out)
	}
}

// "call resume with one" was the only line in this server aimed at the model,
// and it sat directly above a list of candidates. A thorough agent reads that
// as "enumerate these", and did.
func TestListProjectsStatesItsRowsRatherThanInstructingTheModel(t *testing.T) {
	vault := t.TempDir()
	seedScopeCheckpoint(t, vault, "kestrel")
	srv := &Server{DB: testDB(t), vault: vault}

	out, err := srv.listProjects()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "call resume with one") {
		t.Errorf("a tool result instructs the model to enumerate the list beneath it:\n%s", out)
	}
}

// The heading asserted every row had checkpoints while printing rows reading
// "(0 checkpoints)" — and those empty rows are the ghost projects a host
// leaves in any folder it was opened in, so the list advertised its own
// exhaust as work worth resuming.
func TestListProjectsDropsScopesHoldingNoCheckpoint(t *testing.T) {
	vault := t.TempDir()
	seedScopeCheckpoint(t, vault, "kestrel")
	ghost := filepath.Join(vault, session.CheckpointDir, "portfoliowebsite")
	if err := os.MkdirAll(ghost, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ghost, session.NotesFile), []byte("# uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := &Server{DB: testDB(t), vault: vault}

	out, err := srv.listProjects()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "portfoliowebsite") {
		t.Errorf("a scope with no checkpoint is listed as one that has them:\n%s", out)
	}
	if strings.Contains(out, "0 checkpoint") {
		t.Errorf("the heading says these scopes hold checkpoints and a row says none:\n%s", out)
	}
	if !strings.Contains(out, "kestrel") {
		t.Errorf("the scope that does hold a checkpoint was dropped too:\n%s", out)
	}
}
