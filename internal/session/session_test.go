package session

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/vault"

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

func TestWorkingNotesSurviveEvenWhenStateIsWritten(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	AddNote(db, "kestrel-one", "claude", "Meridian will not move below 10k units")
	c := &Checkpoint{
		Project: "kestrel-one", Agent: "claude",
		State: "The gap is really $34 once tariffs are counted.",
		Next:  "get Dana a decision",
	}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}

	// Commit closes the session, so a note not folded in here is unreachable
	// afterwards — dropping it would be silent data loss.
	if !strings.Contains(c.State, "Meridian will not move") {
		t.Errorf("working note lost when state was also written:\n%s", c.State)
	}
	if !strings.Contains(c.State, "tariffs are counted") {
		t.Errorf("the agent's own state was overwritten:\n%s", c.State)
	}
}

// The scenario two live agents actually produced: agent A works, records
// findings, and is killed before it can checkpoint. Agent B arrives, continues,
// and checkpoints.
//
// The bug this pins down: Commit used to bind to whatever session was open on
// the project, so B inherited A's dead session and the handoff record — filename
// and session id — was attributed to A.
func TestCheckpointAfterAnotherAgentDiedIsAttributedCorrectly(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	// Agent A works and dies. No checkpoint.
	AddNote(db, "ota-firmware", "claude", "no flash size anywhere in the vault")
	AddNote(db, "ota-firmware", "claude", "certification is not the real objection")

	// Agent B picks it up, adds its own finding, and checkpoints.
	AddNote(db, "ota-firmware", "cursor", "the eMMC is 64GB — A/B slots cost nothing")
	c := &Checkpoint{
		Project: "ota-firmware", Agent: "cursor",
		Task: "decide whether OTA is viable", Next: "ask Tomas about the boot ROM",
	}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}

	if !strings.HasSuffix(c.Session, "-cursor") {
		t.Errorf("checkpoint filed under %q — a handoff record must name its author", c.Session)
	}
	if !strings.HasSuffix(c.Slug, "-cursor") {
		t.Errorf("slug %q should name the committing agent", c.Slug)
	}

	// A's findings are the thing B is building on. They must reach the durable
	// record, attributed, or they die with the session that gets closed.
	for _, want := range []string{"(claude) no flash size", "(claude) certification is not"} {
		if !strings.Contains(c.State, want) {
			t.Errorf("missing %q from the checkpoint state:\n%s", want, c.State)
		}
	}
	// B's own note needs no attribution — it is the author.
	if !strings.Contains(c.State, "- the eMMC is 64GB") {
		t.Errorf("the committing agent's own note should be unattributed:\n%s", c.State)
	}

	// And nothing may still look uncommitted, or every future resume replays
	// work that has already been written down.
	left, _ := Uncommitted(db, "ota-firmware")
	if len(left) != 0 {
		t.Errorf("%d notes still uncommitted after a checkpoint", len(left))
	}
}

// Two agents working the same project concurrently keep separate sessions.
func TestSessionsAreScopedToTheAgent(t *testing.T) {
	db := testDB(t)

	a, err := Current(db, "brain", "claude")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Current(db, "brain", "cursor")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("two agents must not share one session")
	}
	again, _ := Current(db, "brain", "claude")
	if again.ID != a.ID {
		t.Error("the same agent should stay in its own open session")
	}
}
