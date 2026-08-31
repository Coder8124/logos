package session

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// The join this feature rests on has existed since the first checkpoint —
// decisions and dead ends recorded alongside the files touched while reaching
// them — and nothing ever queried it. These tests pin the two halves that decide
// whether it is useful in practice: that it finds the right checkpoints, and
// that it finds them when the path is spelled differently than it was recorded.

func whyVault(t *testing.T) (string, *sql.DB) {
	t.Helper()
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
