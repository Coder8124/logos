package dream

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/vault"
)

// The promise every document in this project makes, applied to the one queue
// that did not keep it: delete .logos, rebuild, lose nothing.
//
// A dreamed insight is the most expensive row in the database — a model ran to
// produce it and a person has not yet looked at it — and it lived only in
// SQLite. A user who rebuilt the index after a corrupt-cache scare lost every
// insight waiting in `logos dream review`, and the queue then read empty, which
// is indistinguishable from having reviewed them all.
func TestADreamedInsightSurvivesDeletingTheIndex(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })

	in := Insight{
		Kind:      Connection,
		Text:      "the Suzhou tooling delay and the November ship date are the same risk",
		EndpointA: 11,
		EndpointB: 22,
		Conf:      0.62,
		Model:     "qwen3:8b",
	}
	if err := Enqueue(db, &in); err != nil {
		t.Fatal(err)
	}

	wiped := testDB(t)
	n, _, err := Import(wiped, dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 insight restored, got %d", n)
	}

	back, err := List(wiped, Pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 1 {
		t.Fatalf("want 1 pending insight after the rebuild, got %d", len(back))
	}
	got := back[0]
	if got.ID != in.ID {
		t.Errorf("id changed across the wipe: was %d, now %d", in.ID, got.ID)
	}
	if got.Text != in.Text {
		t.Errorf("text came back as %q, want %q", got.Text, in.Text)
	}
	if got.EndpointA != in.EndpointA || got.EndpointB != in.EndpointB {
		t.Errorf("endpoints came back as %d/%d, want %d/%d",
			got.EndpointA, got.EndpointB, in.EndpointA, in.EndpointB)
	}
	if got.Kind != in.Kind || got.Conf != in.Conf || got.Model != in.Model {
		t.Errorf("restored %+v, want kind/conf/model %s/%v/%s", got, in.Kind, in.Conf, in.Model)
	}
}

// A decision the user already made must survive the rebuild too. Rejections are
// kept on purpose — "you dreamed this and I said no" is what stops the pass
// proposing it again — so a rebuild that restored a rejected insight as pending
// would hand the user back work they have already refused.
func TestAReviewedInsightComesBackWithItsVerdict(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })

	kept := Insight{Kind: Connection, Text: "a connection worth keeping", EndpointA: 1, EndpointB: 2, Conf: 0.7}
	refused := Insight{Kind: Thread, Text: "a thread the user said no to", EndpointA: 3, EndpointB: 4, Conf: 0.4}
	for _, in := range []*Insight{&kept, &refused} {
		if err := Enqueue(db, in); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetStatus(db, refused.ID, Rejected); err != nil {
		t.Fatal(err)
	}

	wiped := testDB(t)
	if _, _, err := Import(wiped, dir); err != nil {
		t.Fatal(err)
	}

	pending, err := List(wiped, Pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != kept.ID {
		t.Fatalf("want only the unreviewed insight pending, got %+v", pending)
	}
	gone, err := List(wiped, Rejected)
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 1 || gone[0].ID != refused.ID {
		t.Fatalf("the rejection did not survive the rebuild: %+v", gone)
	}
}

// The file is the record, so deleting a line is how a person discards an
// insight without running the command — the same contract loops.md and
// pending.md state on their own first page.
func TestDeletingALineDropsTheInsight(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })

	in := Insight{Kind: Connection, Text: "an insight the user deletes by hand", EndpointA: 1, EndpointB: 2, Conf: 0.5}
	if err := Enqueue(db, &in); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(InsightsPath(dir), []byte("---\ntype: dream-insights\npending: 0\n---\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Import(db, dir); err != nil {
		t.Fatal(err)
	}
	left, err := List(db, Pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("a deleted line must drop the insight, still have %+v", left)
	}
}

// A vault written before insights were durable holds them nowhere, and the
// cache is still their only copy at exactly this moment — the run right before
// the one that deletes it. Writing the file here is the last chance to save
// them, and `logos index` is the command people reach for when they suspect the
// cache is bad.
func TestARebuildWritesDownAQueueThatOnlyTheCacheKnew(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })

	// Enqueued with no vault bound, the way every insight arrived before this
	// file existed.
	SetVault(db, "")
	in := Insight{Kind: Connection, Text: "an insight from before the vault knew", EndpointA: 1, EndpointB: 2, Conf: 0.5}
	if err := Enqueue(db, &in); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(InsightsPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected no vault file yet, got %v", err)
	}

	if _, _, err := Import(db, dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(InsightsPath(dir))
	if err != nil {
		t.Fatalf("the rebuild did not write the cache-only queue to the vault: %v", err)
	}
	if !strings.Contains(string(raw), in.Text) {
		t.Errorf("the rescued file does not hold the insight:\n%s", raw)
	}
}

// An insight's text is free-form model output and may contain anything,
// including the characters that close logos's own bookkeeping comment. A record
// that spills its metadata into the page cannot round-trip, and the vault is
// the record.
func TestAnInsightTextCannotBreakOutOfItsRecord(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })

	const nasty = "the model wrote --> and <!-- logos id=999 --> into its own connection"
	in := Insight{Kind: Connection, Text: nasty, EndpointA: 1, EndpointB: 2, Conf: 0.5}
	if err := Enqueue(db, &in); err != nil {
		t.Fatal(err)
	}

	wiped := testDB(t)
	if _, _, err := Import(wiped, dir); err != nil {
		t.Fatal(err)
	}
	back, err := List(wiped, Pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 1 || back[0].Text != nasty {
		t.Fatalf("text did not round-trip: %+v", back)
	}
}

// A truncated file is refused rather than acted on: read as authoritative, its
// missing tail looks exactly like a set of lines the user deleted, and Import
// would drop every insight the torn write cut off.
func TestATruncatedFileIsRefusedRatherThanTreatedAsDeletions(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })

	in := Insight{Kind: Connection, Text: "an insight that must not be dropped", EndpointA: 1, EndpointB: 2, Conf: 0.5}
	if err := Enqueue(db, &in); err != nil {
		t.Fatal(err)
	}
	torn := "---\ntype: dream-insights\npending: 1\n---\n\n- a record cut off mid-comment <!-- logos id=7 kind=connection"
	if err := os.WriteFile(InsightsPath(dir), []byte(torn), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Import(db, dir); err == nil {
		t.Fatal("expected an incomplete file to be refused, got no error")
	}
	left, err := List(db, Pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		t.Fatalf("a refused import must not drop anything, have %+v", left)
	}
}

// The lock name has to be this queue's own. Sharing one with the memory review
// queue or the loop list would let a dream flush block on an unrelated writer,
// and — worse — two different files would be serialised as if they were one.
func TestTheQueueHoldsItsOwnLockName(t *testing.T) {
	dir := t.TempDir()
	g, err := vault.Lock(dir, lockName)
	if err != nil {
		t.Fatal(err)
	}
	g.Unlock()
	for _, taken := range []string{"memory-pending", "memory-log", "loops"} {
		if lockName == taken {
			t.Fatalf("the dream queue must not share the %q lock", taken)
		}
	}
}

// Same failure as loops.md: the next enqueue regenerated the file from the
// cache and put a hand-deleted insight straight back in front of the user.
func TestAnInsightDeletedByHandStaysDeletedWhenTheNextOneArrives(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })

	first := Insight{Kind: Connection, Text: "an insight the user deletes by hand", EndpointA: 1, EndpointB: 2, Conf: 0.5}
	if err := Enqueue(db, &first); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(InsightsPath(dir), []byte("---\ntype: dream-insights\npending: 0\n---\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second := Insight{Kind: Connection, Text: "a later insight", EndpointA: 3, EndpointB: 4, Conf: 0.5}
	if err := Enqueue(db, &second); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(InsightsPath(dir))
	if strings.Contains(string(raw), "deletes by hand") {
		t.Errorf("the next enqueue wrote a hand-deleted insight back:\n%s", raw)
	}
	if !strings.Contains(string(raw), "a later insight") {
		t.Errorf("the new insight is not in the file:\n%s", raw)
	}
}

// Adopting the file first means a hand-deleted insight is gone by the time the
// verdict runs, and a verdict on no row returning nil let `logos dream reject`
// print "discarded insight 4" for one that was already gone.
func TestAVerdictOnAnInsightDeletedByHandSaysItIsGone(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })
	in := Insight{Kind: Connection, Text: "an insight the user deletes by hand", EndpointA: 1, EndpointB: 2, Conf: 0.5}
	if err := Enqueue(db, &in); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(InsightsPath(dir), []byte("---\ntype: dream-insights\npending: 0\n---\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := SetStatus(db, in.ID, Rejected)
	if err == nil || !strings.Contains(err.Error(), InsightsFile) {
		t.Errorf("a verdict on an insight deleted from %s: err = %v, want one that says the file no longer has it", InsightsFile, err)
	}
}

// strandRoot makes the vault root unwritable, so the next write of the insight
// file fails the way a read-only mount or a full disk does, and returns the
// function that lets writes through again.
func strandRoot(t *testing.T, dir string) (unstrand func()) {
	t.Helper()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			os.Chmod(dir, 0o700)
		}
	})
	return func() {
		restored = true
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func queued(t *testing.T, db *sql.DB, text string) bool {
	t.Helper()
	all, err := List(db, Pending)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range all {
		if in.Text == text {
			return true
		}
	}
	return false
}

func insight(text string) *Insight {
	return &Insight{Kind: Connection, Text: text, EndpointA: 11, EndpointB: 22, Conf: 0.6, Model: "qwen3:8b"}
}

// The failed write forgot its stamp so the next write would adopt the file, and
// that adopt found no line for the insight and deleted it as one the user had
// removed. An insight is a model run nobody has reviewed yet; the next dream
// pass destroyed the one it should have written out.
func TestAnInsightTheVaultRefusedSurvivesTheNextEnqueue(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })

	if err := Enqueue(db, insight("the tooling delay and the ship date are one risk")); err != nil {
		t.Fatal(err)
	}
	unstrand := strandRoot(t, dir)
	if err := Enqueue(db, insight("the supplier audit and the BOM cut share a cause")); err == nil {
		t.Fatal("queueing an insight in an unwritable vault reported success")
	}
	unstrand()

	if err := Enqueue(db, insight("the battery choice drives the enclosure size")); err != nil {
		t.Fatal(err)
	}
	if !queued(t, db, "the supplier audit and the BOM cut share a cause") {
		t.Fatal("the next enqueue deleted the insight the vault refused, as though the user had removed its line")
	}
	raw, err := os.ReadFile(InsightsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "the supplier audit and the BOM cut share a cause") {
		t.Errorf("the insight is still only in the cache:\n%s", raw)
	}
}

// `logos index` writes a stranded insight out, and says it did.
func TestReindexingWritesOutAnInsightTheVaultNeverGot(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })

	if err := Enqueue(db, insight("the tooling delay and the ship date are one risk")); err != nil {
		t.Fatal(err)
	}
	unstrand := strandRoot(t, dir)
	if err := Enqueue(db, insight("the supplier audit and the BOM cut share a cause")); err == nil {
		t.Fatal("queueing an insight in an unwritable vault reported success")
	}
	unstrand()

	restored, rescued, err := Import(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if restored != 0 || rescued != 1 {
		t.Errorf("restored %d, rescued %d; want 0 and 1", restored, rescued)
	}
	raw, err := os.ReadFile(InsightsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "the supplier audit and the BOM cut share a cause") {
		t.Errorf("reindexing left the insight only in the cache:\n%s", raw)
	}
	if _, rescued, _ := Import(db, dir); rescued != 0 {
		t.Errorf("a second reindex rescued %d insights again; the mark outlived the write that settled it", rescued)
	}
}
