package session

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/gitstate"
	_ "modernc.org/sqlite"
)

// The join this feature rests on has existed since the first checkpoint —
// decisions and dead ends recorded alongside the files touched while reaching
// them — and nothing ever queried it. These tests pin the two halves that decide
// whether it is useful in practice: that it finds the right checkpoints, and
// that it finds them when the path is spelled differently than it was recorded.

func whyVault(t *testing.T) (string, *sql.DB) {
	t.Helper()
	// Commit reads the git state of the working directory, so a checkpoint
	// written from inside this repository absorbs whatever the developer has
	// uncommitted — and Touching matches on those paths too, by design (a file
	// git noticed you changed is a file the session touched). That made these
	// tests pass or fail on the state of someone's editor: editing
	// internal/memory/vaultstore.go and running the suite matched a checkpoint
	// that never named it. Somewhere with no repository at all is the only
	// place the join under test is the only thing being tested.
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	return dir, db
}

func write(t *testing.T, db *sql.DB, dir string, c *Checkpoint) {
	t.Helper()
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}
}

func TestTouchingFindsTheDecisionBehindAFile(t *testing.T) {
	dir, db := whyVault(t)

	write(t, db, dir, &Checkpoint{
		Project:   "memory-store",
		Agent:     "claude",
		Task:      "make the vault write durable",
		Decisions: []string{"flush() returns an error now"},
		Failed:    []string{"keeping vectors in the markdown"},
		Files:     []string{"internal/memory/vaultstore.go"},
	})
	write(t, db, dir, &Checkpoint{
		Project: "api",
		Agent:   "codex",
		Failed:  []string{"connection pooling in the worker"},
		Files:   []string{"internal/api/worker.go"},
	})

	got, err := Touching(dir, "internal/memory/vaultstore.go", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d checkpoints, want 1", len(got))
	}
	if got[0].Project != "memory-store" {
		t.Errorf("matched project %q, want memory-store", got[0].Project)
	}
	// The dead end is the half people are actually asking for.
	if len(got[0].Failed) == 0 || got[0].Failed[0] != "keeping vectors in the markdown" {
		t.Errorf("the ruled-out approach did not survive: %v", got[0].Failed)
	}
}

// A file touched by another project must not be attributed to this one — that
// would be worse than returning nothing, because it reads as an explanation.
func TestTouchingDoesNotMatchUnrelatedFiles(t *testing.T) {
	dir, db := whyVault(t)
	write(t, db, dir, &Checkpoint{
		Project: "api",
		Failed:  []string{"connection pooling"},
		Files:   []string{"internal/api/worker.go"},
	})

	got, err := Touching(dir, "internal/memory/vaultstore.go", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("matched %d unrelated checkpoints: %+v", len(got), got)
	}
}

// The whole feature turns on this. An agent writes whatever path it had in hand
// and a user asks with whatever their shell completed; requiring those to be
// equal means the lookup almost never fires, and a lookup that usually returns
// nothing is one people stop running.
func TestTouchingMatchesHowPathsAreActuallyWritten(t *testing.T) {
	dir, db := whyVault(t)
	write(t, db, dir, &Checkpoint{
		Project: "memory-store",
		Failed:  []string{"keeping vectors in the markdown"},
		Files:   []string{"internal/memory/vaultstore.go"},
	})

	for _, query := range []string{
		"internal/memory/vaultstore.go",               // exact
		"./internal/memory/vaultstore.go",             // shell-completed
		"memory/vaultstore.go",                        // partial
		"vaultstore.go",                               // bare filename
		"/Users/x/proj/internal/memory/vaultstore.go", // absolute
		"internal\\memory\\vaultstore.go",             // windows
	} {
		got, err := Touching(dir, query, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Errorf("query %q found %d, want 1", query, len(got))
		}
	}
}

// Being generous about paths must not become being wrong about them: a suffix
// that is not on a segment boundary is a different file.
func TestTouchingDoesNotMatchPartialSegments(t *testing.T) {
	dir, db := whyVault(t)
	write(t, db, dir, &Checkpoint{
		Project:   "memory-store",
		Decisions: []string{"a decision, because Commit refuses an empty checkpoint"},
		Files:     []string{"internal/memory/vaultstore.go"},
	})

	for _, query := range []string{"store.go", "ultstore.go", "memory/store.go"} {
		got, err := Touching(dir, query, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("query %q wrongly matched vaultstore.go", query)
		}
	}
}

// Newest first: the most recent decision is the one still in force, and reading
// an old one as current is the failure mode that would make this untrustworthy.
func TestTouchingReturnsNewestFirst(t *testing.T) {
	dir, db := whyVault(t)

	write(t, db, dir, &Checkpoint{
		Project:   "memory-store",
		Decisions: []string{"the old decision"},
		Files:     []string{"internal/memory/vaultstore.go"},
		TS:        1000,
	})
	write(t, db, dir, &Checkpoint{
		Project:   "memory-store",
		Decisions: []string{"the current decision"},
		Files:     []string{"internal/memory/vaultstore.go"},
		TS:        9000,
	})

	got, err := Touching(dir, "internal/memory/vaultstore.go", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d, want 2", len(got))
	}
	if got[0].Decisions[0] != "the current decision" {
		t.Errorf("newest first is not holding: got %q", got[0].Decisions[0])
	}
}

func TestTouchingOnAnEmptyVault(t *testing.T) {
	got, err := Touching(t.TempDir(), "anything.go", 0)
	if err != nil {
		t.Fatalf("an empty vault should not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("found %d checkpoints in an empty vault", len(got))
	}
}

// `brain why` joins a path against what a checkpoint touched. Before git's
// observed file list was persisted, the only list it could search was the
// agent's own `Files` — which the CLI cannot set and MCP leaves optional — so
// the command reported "no checkpoint mentions this file" about a file the
// checkpoint in front of it had recorded as changed.
func TestWhyFindsAFileOnlyGitObserved(t *testing.T) {
	v := t.TempDir()
	c := Checkpoint{
		Project: "kestrel",
		Agent:   "cli",
		Session: "20260101-000000-cli",
		Task:    "add annual billing",
		TS:      time.Now().Unix(),
		Git: gitstate.State{
			Branch: "feat/annual-billing",
			Commit: "e30c6b5",
			Dirty:  1,
			Files:  []string{"src/lib/pricing.ts"},
		},
	}
	dir := filepath.Join(v, "sessions", "kestrel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	note := c.Markdown("")
	if !strings.Contains(note, "touched:") {
		t.Fatalf("the observed file list was not written to the note:\n%s", note)
	}
	if err := os.WriteFile(filepath.Join(dir, c.Session+".md"), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}

	// It has to survive the round trip, not merely be written.
	if got := ParseCheckpoint(note); len(got.Git.Files) != 1 || got.Git.Files[0] != "src/lib/pricing.ts" {
		t.Fatalf("touched did not round-trip: %+v", got.Git.Files)
	}

	hits, err := Touching(v, "src/lib/pricing.ts", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("why found %d checkpoints for a file git recorded as changed; want 1", len(hits))
	}
	if hits[0].Task != "add annual billing" {
		t.Errorf("matched the wrong checkpoint: %q", hits[0].Task)
	}
}
