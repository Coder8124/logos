package session

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pragun/brain/internal/vault"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestNotesAccumulateInOneOpenSession(t *testing.T) {
	db := testDB(t)

	if _, err := AddNote(db, "kestrel-one", "claude", "re-quoted the waveguide"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddNote(db, "kestrel-one", "claude", "no movement under 10k units"); err != nil {
		t.Fatal(err)
	}

	// A second note must not open a second session, or the trail fragments.
	var sessions int
	db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions)
	if sessions != 1 {
		t.Fatalf("notes should share one open session, got %d sessions", sessions)
	}

	notes, err := Uncommitted(db, "kestrel-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 || notes[0].Text != "re-quoted the waveguide" {
		t.Fatalf("working notes lost or reordered: %+v", notes)
	}
}

func TestCommitClosesTheSessionAndClearsUncommitted(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	AddNote(db, "kestrel-one", "claude", "tried the dual-mic drop")
	c := &Checkpoint{Project: "kestrel-one", Agent: "claude", Task: "cut the BOM", Next: "get a firm quote"}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}

	// Committed work is no longer uncommitted — that is what a commit means.
	left, _ := Uncommitted(db, "kestrel-one")
	if len(left) != 0 {
		t.Errorf("commit should close the session, %d notes still uncommitted", len(left))
	}
	if c.Slug != "sessions/kestrel-one/"+c.Session {
		t.Errorf("unexpected slug %q", c.Slug)
	}
	if _, err := os.Stat(filepath.Join(dir, c.Slug+".md")); err != nil {
		t.Errorf("checkpoint note not written: %v", err)
	}
}

func TestCommitFoldsWorkingNotesIntoStateWhenStateIsBlank(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	AddNote(db, "kestrel-one", "claude", "yield still 71% on the revised bond line")
	c := &Checkpoint{Project: "kestrel-one", Agent: "claude", Next: "re-run at 500 units"}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.State, "yield still 71%") {
		t.Errorf("an agent that only left working notes should still get a useful checkpoint, got state %q", c.State)
	}
}

func TestCommitRefusesAnEmptyCheckpoint(t *testing.T) {
	db := testDB(t)
	if err := Commit(db, t.TempDir(), &Checkpoint{Project: "kestrel-one", Agent: "claude"}); err == nil {
		t.Error("an empty checkpoint is worse than none — it looks like a handoff and carries nothing")
	}
}

func TestLatestReadsTheVaultNotTheIndex(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	Commit(db, dir, &Checkpoint{Project: "kestrel-one", Agent: "claude", Task: "first", Next: "a"})
	Commit(db, dir, &Checkpoint{Project: "kestrel-one", Agent: "cursor", Task: "second", Next: "b"})

	// The whole point of markdown-as-truth: throw the database away entirely.
	db.Close()

	got, err := Latest(dir, "kestrel-one")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Task != "second" {
		t.Fatalf("latest checkpoint must survive without the index, got %+v", got)
	}
	if got.Agent != "cursor" {
		t.Errorf("checkpoint lost its author: %q", got.Agent)
	}
}

func TestLatestOnAProjectWithNoCheckpoints(t *testing.T) {
	got, err := Latest(t.TempDir(), "nothing-here")
	if err != nil || got != nil {
		t.Fatalf("want (nil, nil) for an unknown project, got (%+v, %v)", got, err)
	}
}

func TestSecondCheckpointLinksBackToTheFirst(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	first := &Checkpoint{Project: "kestrel-one", Agent: "claude", Task: "first", Next: "a"}
	Commit(db, dir, first)
	second := &Checkpoint{Project: "kestrel-one", Agent: "cursor", Task: "second", Next: "b"}
	Commit(db, dir, second)

	raw, err := os.ReadFile(filepath.Join(dir, second.Slug+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "pred: follows, obj: \"[["+first.Session+"]]\"") {
		t.Errorf("checkpoints must chain, so the graph shows the sequence of work:\n%s", raw)
	}
}

func TestCheckpointIsAParseableVaultNote(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	c := &Checkpoint{
		Project: "kestrel-one", Agent: "claude",
		Task: "cut the BOM to the $118 target", Next: "quote the single-mic line",
	}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, c.Slug+".md")
	raw, _ := os.ReadFile(path)
	n := vault.Parse(dir, path, string(raw))

	// If the indexer cannot read this as a checkpoint linked to its project,
	// none of the retrieval benefits of using markdown actually land.
	if n.Kind != "checkpoint" {
		t.Errorf("kind = %q, want checkpoint", n.Kind)
	}
	var linked bool
	for _, e := range n.Edges {
		if e.Pred == "checkpoint_of" && e.Obj == "kestrel-one" {
			linked = true
		}
	}
	if !linked {
		t.Errorf("checkpoint is not edged to its project: %+v", n.Edges)
	}
	if !strings.Contains(n.Body, "cut the BOM") {
		t.Errorf("task missing from the indexed body: %q", n.Body)
	}
}

func TestMarkdownRoundTrip(t *testing.T) {
	want := Checkpoint{
		Session: "20260814-143207-claude",
		Project: "kestrel-one",
		Agent:   "claude",
		Task:    "cut the BOM from $141.20 to the $118 target",
		State:   "Three lines are over plan.\n\nThe display stack is the worst of them.",
		Decisions: []string{
			"dropped the dual-mic array, saves $4.10",
			"holding the 43g weight target",
		},
		Failed:    []string{"re-quoting the waveguide: no movement under 10k units"},
		Questions: []string{"does 71% bonding yield hold on the revised line?"},
		Files:     []string{"projects/kestrel-one.md", "topics/bom-cost.md"},
		Next:      "get a firm quote on the single-mic BOM line before Friday",
		HandoffTo: "cursor",
		TS:        1755172800,
	}

	got := ParseCheckpoint(want.Markdown(""))
	got.TS = want.TS // first_seen is day-resolution by design

	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip lost information:\n got %+v\nwant %+v", got, want)
	}
}

func TestRoundTripSurvivesAColonInTheTask(t *testing.T) {
	// Unquoted YAML would swallow this into a mapping and lose the title.
	c := Checkpoint{Project: "brain", Agent: "claude", Task: "fix: the panel: broken", Next: "ship"}
	got := ParseCheckpoint(c.Markdown(""))
	if got.Task != c.Task {
		t.Errorf("task = %q, want %q", got.Task, c.Task)
	}
}

func TestSafeRejectsPathEscapes(t *testing.T) {
	// Project names arrive from agents; a traversal must not reach the vault.
	for _, in := range []string{"../../etc/passwd", "Kestrel One", "projects/kestrel-one"} {
		got := safe(in)
		if strings.Contains(got, "..") || strings.ContainsAny(got, `/\`) {
			t.Errorf("safe(%q) = %q — still usable as a path escape", in, got)
		}
	}
	if got := safe("Kestrel One"); got != "kestrel-one" {
		t.Errorf(`safe("Kestrel One") = %q, want "kestrel-one"`, got)
	}
}

func TestRapidCheckpointsByOneAgentDoNotOverwriteEachOther(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	// Same agent, same second: the ids must still differ, or the second
	// checkpoint silently replaces the first one's file.
	for i := range 3 {
		c := &Checkpoint{Project: "brain", Agent: "claude", Task: "step", Next: string(rune('a' + i))}
		if err := Commit(db, dir, c); err != nil {
			t.Fatal(err)
		}
	}
	hist, err := History(dir, "brain", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 {
		t.Fatalf("want 3 distinct checkpoints, got %d", len(hist))
	}
	if hist[0].Next != "c" {
		t.Errorf("history should be newest first, got Next=%q", hist[0].Next)
	}
}
