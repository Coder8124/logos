package session

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// boundDB is a database that knows which vault it belongs to, the way one
// opened through internal/index does.
func boundDB(t *testing.T, vault string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); SetVault(db, "") })
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	SetVault(db, vault)
	return db
}

// The documentation tells people the index is a cache and that deleting it is
// safe. For working notes it was not: they lived in session_notes and nowhere
// else, so the reindex that restored every memory and checkpoint threw away
// exactly the in-flight work note_progress promises to keep — and said nothing.
func TestWorkingNotesSurviveDeletingTheIndex(t *testing.T) {
	v := t.TempDir()
	db := boundDB(t, v)

	for _, text := range []string{
		"the annual toggle belongs in PricingTable, confirmed with design",
		"vitest needs jsdom for the PricingTable test",
	} {
		if _, err := AddNote(db, "kestrel", "claude", text); err != nil {
			t.Fatal(err)
		}
	}

	// Simulate `rm -rf .brain && brain index`: a wholly new database, same vault.
	rebuilt := boundDB(t, v)
	n, err := ImportNotes(rebuilt, v)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("restored %d notes, want 2", n)
	}

	got, err := Uncommitted(rebuilt, "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("after the rebuild the project has %d uncommitted notes, want 2", len(got))
	}
	if !strings.Contains(got[0].Text, "PricingTable") || got[0].Agent != "claude" {
		t.Errorf("note came back wrong: %+v", got[0])
	}
	if got[0].TS == 0 {
		t.Error("a restored note lost its timestamp, so how old it is became a lie")
	}
}

// Running index twice is ordinary — the watch loop does it every two seconds.
// Importing has to add what is missing and nothing else.
func TestRestoringNotesTwiceDoesNotDoubleThem(t *testing.T) {
	v := t.TempDir()
	db := boundDB(t, v)
	if _, err := AddNote(db, "kestrel", "claude", "jsdom, not happy-dom"); err != nil {
		t.Fatal(err)
	}

	rebuilt := boundDB(t, v)
	for i := range 3 {
		n, err := ImportNotes(rebuilt, v)
		if err != nil {
			t.Fatal(err)
		}
		if want := 1; i == 0 && n != want {
			t.Fatalf("first import restored %d, want %d", n, want)
		} else if i > 0 && n != 0 {
			t.Fatalf("import %d restored %d notes that were already there", i+1, n)
		}
	}
	got, _ := Uncommitted(rebuilt, "kestrel")
	if len(got) != 1 {
		t.Fatalf("%d notes after three imports, want 1", len(got))
	}
}

// A checkpoint folds the notes into a permanent record and closes the sessions.
// Leaving the file behind would claim work is still outstanding that is not,
// and would resurrect it as uncommitted on the next rebuild.
func TestCheckpointClearsTheWorkingNotesFile(t *testing.T) {
	v := t.TempDir()
	db := boundDB(t, v)
	if _, err := AddNote(db, "kestrel", "claude", "annual billing wired up"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(v, CheckpointDir, "kestrel", NotesFile)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a note was written but no working-notes file exists: %v", err)
	}

	c := Checkpoint{Project: "kestrel", Agent: "claude", State: "done", Next: "ship"}
	if err := Commit(db, v, &c); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the working-notes file outlived the checkpoint that captured it")
	}

	rebuilt := boundDB(t, v)
	if n, err := ImportNotes(rebuilt, v); err != nil || n != 0 {
		t.Errorf("a rebuild resurrected %d checkpointed notes (%v)", n, err)
	}
}

// Worktrees are a sub-scope — sessions/<project>/<worktree> — and their loose
// ends have to file beside their own checkpoints, not their parent's.
func TestWorktreeNotesFileBesideTheirOwnCheckpoints(t *testing.T) {
	v := t.TempDir()
	db := boundDB(t, v)
	if _, err := AddNote(db, "kestrel/feature-x", "claude", "worktree-only finding"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(v, CheckpointDir, "kestrel", "feature-x", NotesFile)); err != nil {
		t.Fatalf("worktree notes were not filed under the worktree: %v", err)
	}

	rebuilt := boundDB(t, v)
	if _, err := ImportNotes(rebuilt, v); err != nil {
		t.Fatal(err)
	}
	if got, _ := Uncommitted(rebuilt, "kestrel/feature-x"); len(got) != 1 {
		t.Errorf("worktree scope came back with %d notes, want 1", len(got))
	}
	if got, _ := Uncommitted(rebuilt, "kestrel"); len(got) != 0 {
		t.Errorf("a worktree's notes leaked into its parent project: %d", len(got))
	}
}

// A note holding a newline would render as a bullet the parser cannot read
// back — saved to the eye, lost on the rebuild.
func TestAMultilineNoteStillComesBack(t *testing.T) {
	v := t.TempDir()
	db := boundDB(t, v)
	if _, err := AddNote(db, "kestrel", "claude", "two\nlines\tand a tab"); err != nil {
		t.Fatal(err)
	}
	rebuilt := boundDB(t, v)
	if _, err := ImportNotes(rebuilt, v); err != nil {
		t.Fatal(err)
	}
	got, _ := Uncommitted(rebuilt, "kestrel")
	if len(got) != 1 || got[0].Text != "two lines and a tab" {
		t.Errorf("multiline note came back as %+v", got)
	}
}

// Nothing bound, nothing written: a caller that only wants the cache — a test,
// an eval harness — must not fail or start writing files somewhere.
func TestUnboundDatabaseStillTakesNotes(t *testing.T) {
	db := testDB(t)
	if _, err := AddNote(db, "kestrel", "claude", "no vault here"); err != nil {
		t.Fatalf("an unbound database refused a note: %v", err)
	}
}

// "Most recent checkpoint" is a reverse sort of the filenames in a session
// directory, so any file that is not a timestamped checkpoint sorts ahead of
// every real one and becomes what resume reports. uncommitted.md is such a
// file, and so is anything a user leaves beside their history.
func TestStrayFilesAreNotMistakenForTheLatestCheckpoint(t *testing.T) {
	v := t.TempDir()
	db := boundDB(t, v)

	c := Checkpoint{Project: "kestrel", Agent: "claude", State: "shipped billing", Next: "ship"}
	if err := Commit(db, v, &c); err != nil {
		t.Fatal(err)
	}
	if _, err := AddNote(db, "kestrel", "claude", "and then this happened"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(v, CheckpointDir, "kestrel")
	if err := os.WriteFile(filepath.Join(dir, "zz-notes-to-self.md"), []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	hist, err := History(v, "kestrel", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 {
		t.Fatalf("history has %d entries, want only the real checkpoint", len(hist))
	}
	latest, err := Latest(v, "kestrel")
	if err != nil || latest == nil {
		t.Fatalf("Latest returned %v, %v", latest, err)
	}
	if !strings.Contains(latest.State, "shipped billing") {
		t.Errorf("Latest returned a file that is not a checkpoint: %+v", latest)
	}
}

// A crash or a full disk partway through a save leaves a checkpoint file that
// parses to nothing. Because it is the newest filename, it used to be the only
// one History looked at — so one torn write made resume report that a project
// with a full history had none.
func TestATornCheckpointDoesNotHideTheGoodOnes(t *testing.T) {
	v := t.TempDir()
	db := boundDB(t, v)

	good := Checkpoint{Project: "kestrel", Agent: "claude", State: "annual billing shipped", Next: "monitor"}
	if err := Commit(db, v, &good); err != nil {
		t.Fatal(err)
	}
	// A later file, truncated mid-frontmatter.
	dir := filepath.Join(v, CheckpointDir, "kestrel")
	torn := filepath.Join(dir, "29991231-235959-claude.md")
	if err := os.WriteFile(torn, []byte("---\ntype: checkpoint\ntitle: kestrel —"), 0o600); err != nil {
		t.Fatal(err)
	}

	latest, err := Latest(v, "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil {
		t.Fatal("a torn checkpoint made an intact history look empty")
	}
	if !strings.Contains(latest.State, "annual billing shipped") {
		t.Errorf("Latest returned the torn file: %+v", latest)
	}
	if hist, _ := History(v, "kestrel", 20); len(hist) != 1 {
		t.Errorf("history has %d entries, want only the intact checkpoint", len(hist))
	}
}
