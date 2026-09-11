package insight

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/session"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := memory.Init(db); err != nil {
		t.Fatal(err)
	}
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// An insight with no source is exactly the fabrication internal/ingest.Filter
// already refuses for a distilled claim — it must never survive to be shown.
func TestFilterDropsAnInsightWithNoSources(t *testing.T) {
	kept, drops := Filter([]Insight{
		{Kind: "test", Text: "something true", Sources: nil},
		{Kind: "test", Text: "something cited", Sources: []string{"memory#1"}},
	})
	if len(kept) != 1 || kept[0].Text != "something cited" {
		t.Fatalf("expected only the cited insight to survive, got %v", kept)
	}
	if len(drops) != 1 || drops[0].Reason != "no source cited" {
		t.Fatalf("expected one drop naming the missing citation, got %v", drops)
	}
}

// A blocker restated across two checkpoints, even in different words, is a
// standing problem — the whole point of this generator is to say so instead
// of making the user notice it themselves.
func TestARecurringBlockerIsReportedWithEveryCheckpointItAppearedIn(t *testing.T) {
	dir := t.TempDir()
	db := testDB(t)

	if err := session.Commit(db, dir, &session.Checkpoint{
		Project:  "kestrel-one",
		Next:     "first",
		Blockers: []string{"the vendor has not shipped the connector firmware"},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // checkpoint filenames are second-resolution
	if err := session.Commit(db, dir, &session.Checkpoint{
		Project:  "kestrel-one",
		Next:     "second",
		Blockers: []string{"still waiting on the connector firmware from the vendor"},
	}); err != nil {
		t.Fatal(err)
	}

	insights, drops, err := Generate(db, dir, "kestrel-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(drops) != 0 {
		t.Fatalf("expected no drops, got %v", drops)
	}
	var found *Insight
	for i := range insights {
		if insights[i].Kind == "recurring-blocker" {
			found = &insights[i]
		}
	}
	if found == nil {
		t.Fatalf("expected a recurring-blocker insight, got %v", insights)
	}
	if len(found.Sources) != 2 {
		t.Fatalf("expected the insight to cite both checkpoints, got %v", found.Sources)
	}
	for _, src := range found.Sources {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(src)+".md")); err != nil {
			t.Fatalf("cited source %q does not exist in the vault: %v", src, err)
		}
	}
}

// A blocker mentioned exactly once is not a pattern, and reporting it as one
// would train the user to ignore every recurring-blocker insight.
func TestABlockerMentionedOnlyOnceIsNotReportedAsRecurring(t *testing.T) {
	dir := t.TempDir()
	db := testDB(t)

	if err := session.Commit(db, dir, &session.Checkpoint{
		Project:  "kestrel-one",
		Next:     "first",
		Blockers: []string{"a one-off problem nobody saw again"},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := session.Commit(db, dir, &session.Checkpoint{
		Project: "kestrel-one",
		Next:    "second",
	}); err != nil {
		t.Fatal(err)
	}

	insights, _, err := Generate(db, dir, "kestrel-one")
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range insights {
		if in.Kind == "recurring-blocker" {
			t.Fatalf("a one-off blocker must not be reported as recurring: %v", in)
		}
	}
}

// A memory nobody has drawn on in months is worth surfacing, and the report
// must cite the exact memory id — an insight that only vaguely gestures at
// "some fact" gives the user nothing to act on.
func TestADormantMemoryIsReportedWithItsMemoryIDAsSource(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	r, err := memory.Store(db, nil, "", &memory.Memory{Text: "the vendor's rep is named Priya", Kind: memory.Fact, Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-100 * 24 * time.Hour).Unix()
	if _, err := db.Exec("UPDATE memories SET last_used = 0, created = ? WHERE id = ?", old, r.ID); err != nil {
		t.Fatal(err)
	}

	insights, _, err := Generate(db, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, in := range insights {
		if in.Kind == "dormant-memory" {
			if len(in.Sources) != 1 || in.Sources[0] != "memory#"+strconv.FormatInt(r.ID, 10) {
				t.Fatalf("expected the insight to cite memory#%d, got %v", r.ID, in.Sources)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a dormant-memory insight, got %v", insights)
	}
}

// A memory the user already excluded from recall (PinNever) has already had
// its verdict delivered — reporting it as dormant would be re-litigating a
// decision, not surfacing a new one.
func TestAnExcludedMemoryIsNeverReportedAsDormant(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	r, err := memory.Store(db, nil, "", &memory.Memory{Text: "an old fact nobody cares about anymore", Kind: memory.Fact, Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-100 * 24 * time.Hour).Unix()
	if _, err := db.Exec("UPDATE memories SET last_used = 0, created = ? WHERE id = ?", old, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := memory.Exclude(db, r.ID); err != nil {
		t.Fatal(err)
	}

	insights, _, err := Generate(db, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range insights {
		if in.Kind == "dormant-memory" {
			t.Fatalf("an excluded memory must never be reported as dormant: %v", in)
		}
	}
}

// A caller has no way to know Generate ran a mechanical-only pass unless the
// package says so somewhere stable enough to print — this is invariant 3
// ("a feature announces itself") applied to a generator that has no fuller
// tier to fall back from, only one it has not built yet.
func TestDegradedNamesTheMechanicalTier(t *testing.T) {
	if Degraded == "" {
		t.Fatal("expected a non-empty degradation notice")
	}
	if !strings.Contains(Degraded, "mechanical") {
		t.Fatalf("expected the notice to name the mechanical tier, got %q", Degraded)
	}
}
