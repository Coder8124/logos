package rename

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRenameMovesEverySurface(t *testing.T) {
	v := seedVault(t)
	db := seedDB(t)

	res, err := Run(db, v, "brain", "logos", false)
	if err != nil {
		t.Fatal(err)
	}
	// Two checkpoints: the project's own and the one in its feature-x worktree,
	// which is a sub-scope of the project rather than a project of its own.
	if res.Checkpoints != 2 || res.Memories != 1 || res.Events != 2 {
		t.Fatalf("counts = %+v; want 2 checkpoints, 1 memory, 2 events", res)
	}

	if _, err := os.Stat(filepath.Join(v, "sessions", "brain")); !os.IsNotExist(err) {
		t.Error("the old session directory still exists")
	}
	got := read(t, filepath.Join(v, "sessions", "logos", "20260101-000000-claude.md"))
	if !strings.Contains(got, "project: logos") {
		t.Errorf("frontmatter project was not renamed:\n%s", got)
	}
	if !strings.Contains(got, "[[logos]]") {
		t.Errorf("the project relation was not renamed:\n%s", got)
	}

	if m := read(t, filepath.Join(v, "memories", "fact.md")); !strings.Contains(m, "project=logos") {
		t.Errorf("memory project field was not renamed:\n%s", m)
	}
	if a := read(t, filepath.Join(v, "activity", "2026-01.jsonl")); strings.Contains(a, `"project":"brain"`) {
		t.Errorf("an activity event kept the old project:\n%s", a)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM memories WHERE project = 'logos'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("index memories not renamed: %d rows, %v", n, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE project = 'logos'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("index sessions not renamed: %d rows, %v", n, err)
	}
}

// The record has to survive the move intact. A checkpoint's prose may name the
// old project — a checkpoint *about* a rename certainly will — and rewriting
// that would falsify history while claiming to relocate it.
func TestRenameLeavesProseAlone(t *testing.T) {
	v := seedVault(t)
	if _, err := Run(nil, v, "brain", "logos", false); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(v, "sessions", "logos", "20260101-000000-claude.md"))
	if !strings.Contains(got, "decided to call brain something else") {
		t.Errorf("the body prose was rewritten by the rename:\n%s", got)
	}
	if m := read(t, filepath.Join(v, "memories", "fact.md")); !strings.Contains(m, "the brain module path") {
		t.Errorf("a memory's text was rewritten by the rename:\n%s", m)
	}
	if a := read(t, filepath.Join(v, "activity", "2026-01.jsonl")); !strings.Contains(a, "rename brain to something") {
		t.Errorf("an event's summary was rewritten by the rename:\n%s", a)
	}
}

// Prefix matching would sweep up every project whose name starts with the one
// being renamed, which is the failure that makes a bulk edit untrustworthy.
func TestRenameDoesNotTouchASimilarlyNamedProject(t *testing.T) {
	v := seedVault(t)
	if _, err := Run(nil, v, "brain", "logos", false); err != nil {
		t.Fatal(err)
	}
	if m := read(t, filepath.Join(v, "memories", "fact.md")); !strings.Contains(m, "project=brain-www") {
		t.Errorf("brain-www was renamed along with brain:\n%s", m)
	}
	if _, err := os.Stat(filepath.Join(v, "sessions", "brain-www")); err != nil {
		t.Error("the brain-www session directory was moved")
	}
}

// A worktree is a sub-scope of its project — sessions/<project>/<worktree> —
// so it travels with the rename rather than being left behind under a name
// that no longer exists.
func TestRenameCarriesWorktreeSubScopes(t *testing.T) {
	v := seedVault(t)
	db := seedDB(t)
	if _, err := Run(db, v, "brain", "logos", false); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(v, "sessions", "logos", "feature-x", "20260102-000000-claude.md"))
	if !strings.Contains(got, "project: logos") {
		t.Errorf("a worktree checkpoint was not renamed:\n%s", got)
	}
	var name string
	if err := db.QueryRow(`SELECT project FROM sessions WHERE id = 'wt'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "logos/feature-x" {
		t.Errorf("worktree scope = %q, want %q", name, "logos/feature-x")
	}
}

// --dry-run has to be trustworthy or it is worse than not existing: it must
// report the real counts and write nothing.
func TestDryRunReportsWithoutWriting(t *testing.T) {
	v := seedVault(t)
	db := seedDB(t)
	res, err := Run(db, v, "brain", "logos", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Checkpoints == 0 || res.Memories == 0 || res.Events == 0 || res.Rows == 0 {
		t.Fatalf("a dry run reported nothing to do: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(v, "sessions", "brain")); err != nil {
		t.Error("a dry run moved the session directory")
	}
	if m := read(t, filepath.Join(v, "memories", "fact.md")); !strings.Contains(m, "project=brain ") {
		t.Error("a dry run rewrote a memory line")
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM memories WHERE project = 'brain'`).Scan(&n)
	if n != 1 {
		t.Error("a dry run updated the index")
	}
}

// Merging two projects asks questions this command does not answer — whose
// checkpoint wins when both have one from the same minute — so it refuses
// rather than interleaving them silently.
func TestRenameRefusesToMergeIntoAnExistingProject(t *testing.T) {
	v := seedVault(t)
	if _, err := Run(nil, v, "brain", "brain-www", false); err == nil {
		t.Fatal("renaming onto an existing project should have failed")
	}
	if _, err := os.Stat(filepath.Join(v, "sessions", "brain")); err != nil {
		t.Error("the refused rename still moved the directory")
	}
}

// A name becomes a directory under sessions/, and "logos/feature-x" is how a
// worktree sub-scope is spelled. A rename must not be able to forge one.
func TestRenameRejectsUnusableNames(t *testing.T) {
	v := seedVault(t)
	for _, bad := range []string{"logos/feature-x", "..", ".hidden", ""} {
		if _, err := Run(nil, v, "brain", bad, false); err == nil {
			t.Errorf("rename to %q should have been rejected", bad)
		}
	}
	if _, err := Run(nil, v, "brain", "brain", false); err == nil {
		t.Error("renaming a project to its own name should have been rejected")
	}
}

// A typo produces zero of everything, which reads exactly like success unless
// the caller can tell the two apart.
func TestEmptyResultIsDistinguishable(t *testing.T) {
	v := seedVault(t)
	res, err := Run(nil, v, "no-such-project", "logos", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Empty() {
		t.Errorf("renaming an unknown project reported work done: %+v", res)
	}
}

func seedVault(t *testing.T) string {
	t.Helper()
	v := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(v, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mk("sessions/brain/20260101-000000-claude.md", `---
type: checkpoint
title: 'brain — get it shipped'
project: brain
agent: claude
relations:
  - { pred: checkpoint_of, obj: "[[brain]]", conf: 1.0, src: stated }
---

## Task

We decided to call brain something else, and wrote that down here.
`)
	mk("sessions/brain/feature-x/20260102-000000-claude.md", `---
type: checkpoint
project: brain
agent: claude
---

## Task

Worktree work.
`)
	mk("sessions/brain-www/20260101-000000-claude.md", `---
type: checkpoint
project: brain-www
agent: claude
---

## Task

The site.
`)
	mk("memories/fact.md", strings.Join([]string{
		"# fact",
		"",
		"- the brain module path is github.com/Coder8124/brain <!-- id=1 project=brain created=1 -->",
		"- the site is separate <!-- id=2 project=brain-www created=1 -->",
		"",
	}, "\n"))
	mk("activity/2026-01.jsonl", strings.Join([]string{
		`{"ts":1,"kind":"prompt","project":"brain","summary":"rename brain to something"}`,
		`{"ts":2,"kind":"tool","project":"brain"}`,
		`{"ts":3,"kind":"prompt","project":"brain-www"}`,
		`not json at all, torn write`,
		"",
	}, "\n"))
	return v
}

func seedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
CREATE TABLE memories (id INTEGER PRIMARY KEY, project TEXT NOT NULL DEFAULT '');
CREATE TABLE memory_log (id INTEGER PRIMARY KEY, project TEXT NOT NULL DEFAULT '');
CREATE TABLE sessions (id TEXT PRIMARY KEY, project TEXT NOT NULL);
INSERT INTO memories (project) VALUES ('brain'), ('brain-www');
INSERT INTO memory_log (project) VALUES ('brain');
INSERT INTO sessions (id, project) VALUES ('a','brain'), ('wt','brain/feature-x'), ('b','brain-www');
`); err != nil {
		t.Fatal(err)
	}
	return db
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The source name is joined onto the vault path and then read, rewritten and
// os.Rename'd, so a "../.." in it walked out of the vault: a real run moved an
// arbitrary directory from elsewhere on disk *into* sessions/ and rewrote the
// frontmatter of the files in it. Both names are path input and neither is
// trusted.
func TestRenameRefusesToEscapeTheVault(t *testing.T) {
	v := seedVault(t)
	outside := t.TempDir()
	victim := filepath.Join(outside, "note.md")
	const body = "---\ntype: checkpoint\nproject: escape\n---\n"
	if err := os.WriteFile(victim, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(filepath.Join(v, "sessions"), outside)
	if err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{rel, "../..", "../../etc", `..\..`, ".hidden", ""} {
		if _, err := Run(nil, v, from, "stolen", false); err == nil {
			t.Errorf("rename from %q should have been rejected", from)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Error("a file outside the vault was moved or deleted by a rename")
	}
	if got := read(t, victim); got != body {
		t.Errorf("a file outside the vault was rewritten by a rename:\n%s", got)
	}
}

// A project can own a session directory before it owns a checkpoint: working
// notes are filed there as they are written. Gating the move on the number of
// checkpoints rewritten left that directory — and the notes in it — behind
// under the old name, while the index had already been renamed. The next
// `brain index` then restored them under a project that no longer exists.
func TestRenameMovesADirectoryThatHasNoCheckpointsYet(t *testing.T) {
	v := t.TempDir()
	dir := filepath.Join(v, "sessions", "kestrel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	const notes = "# uncommitted — kestrel\n\n- the toggle lives in PricingTable <!-- ts=1 agent=claude -->\n"
	if err := os.WriteFile(filepath.Join(dir, "uncommitted.md"), []byte(notes), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(nil, v, "kestrel", "falcon", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the old session directory was left behind by the rename")
	}
	if got := read(t, filepath.Join(v, "sessions", "falcon", "uncommitted.md")); got != notes {
		t.Errorf("working notes did not travel intact:\n%s", got)
	}
}

// A dry run still must not move it.
func TestDryRunDoesNotMoveACheckpointlessDirectory(t *testing.T) {
	v := t.TempDir()
	dir := filepath.Join(v, "sessions", "kestrel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(nil, v, "kestrel", "falcon", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Error("a dry run moved the session directory")
	}
}

// The vault is rewritten before the index, so an index that has never created
// a table must not turn a completed vault rename into a reported failure.
func TestRenameSucceedsOnAnIndexWithNoTablesYet(t *testing.T) {
	v := seedVault(t)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := Run(db, v, "brain", "logos", false); err != nil {
		t.Fatalf("rename against an empty index failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(v, "sessions", "logos")); err != nil {
		t.Error("the vault half of the rename did not happen")
	}
}
