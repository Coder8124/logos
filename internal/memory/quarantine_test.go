package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQuarantinedMemoryIsInvisibleToNormalReads(t *testing.T) {
	db := testDB(t)
	if _, err := Store(db, nil, "", &Memory{
		Text: "an unreviewed fact", Kind: Fact, Source: "mcp", Quarantined: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Store(db, nil, "", &Memory{
		Text: "an active fact", Kind: Fact, Source: "manual",
	}); err != nil {
		t.Fatal(err)
	}

	all, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Text != "an active fact" {
		t.Errorf("All() returned the quarantined memory: %+v", all)
	}

	// Recall with a nil provider falls back to All() (see Recall) — still a
	// real entry point normal callers use, not a test-only shortcut.
	got, err := Recall(db, nil, "", "an unreviewed fact", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.Text == "an unreviewed fact" {
			t.Error("recall surfaced a quarantined memory")
		}
	}

	n, err := Count(db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("Count (the raw row count) = %d, want 2 — quarantine should hide a row from views, not delete it", n)
	}
}

func TestStoreReceiptReportsQuarantined(t *testing.T) {
	db := testDB(t)
	r, err := Store(db, nil, "", &Memory{Text: "proposed fact", Kind: Fact, Source: "mcp", Quarantined: true})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Queued() {
		t.Errorf("Receipt.Queued() = false for a quarantined store, outcome = %q", r.Outcome)
	}
	if r.Created() {
		t.Error("Receipt.Created() should be false for a quarantined store")
	}
}

func TestAcceptReleasesAQuarantinedMemory(t *testing.T) {
	db, dir := store(t) // from durability_test.go — vault-backed
	r, err := Store(db, nil, "", &Memory{Text: "the drop test is 1.2m", Kind: Fact, Source: "mcp", Quarantined: true})
	if err != nil {
		t.Fatal(err)
	}

	// Not on disk yet — quarantine must not leak into the vault before review.
	if _, err := os.Stat(path(dir, Fact)); !os.IsNotExist(err) {
		t.Fatalf("a quarantined memory was written to the vault before acceptance (err=%v)", err)
	}

	if err := Accept(db, r.ID); err != nil {
		t.Fatal(err)
	}

	all, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Quarantined {
		t.Errorf("accepted memory should be active and visible: %+v", all)
	}

	// Now on disk — Accept is what makes it durable.
	raw, err := os.ReadFile(path(dir, Fact))
	if err != nil {
		t.Fatalf("accepted memory never reached the vault: %v", err)
	}
	if !strings.Contains(string(raw), "drop test") {
		t.Errorf("vault file does not contain the accepted memory:\n%s", raw)
	}
}

func TestRejectDiscardsAQuarantinedMemory(t *testing.T) {
	db := testDB(t)
	r, err := Store(db, nil, "", &Memory{Text: "a proposal nobody wants", Kind: Fact, Source: "mcp", Quarantined: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := Reject(db, r.ID); err != nil {
		t.Fatal(err)
	}
	n, _ := Count(db)
	if n != 0 {
		t.Errorf("row count after reject = %d, want 0 — reject should delete, not just hide", n)
	}

	// A history entry survives the deletion, the same guarantee Forget makes.
	hist, err := History(db, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) == 0 || hist[len(hist)-1].Event != EvRejected {
		t.Errorf("expected an EvRejected entry in the timeline, got %+v", hist)
	}
}

func TestAcceptAndRejectRefuseNonPendingMemories(t *testing.T) {
	db := testDB(t)
	r, err := Store(db, nil, "", &Memory{Text: "already active", Kind: Fact, Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Accept(db, r.ID); err == nil {
		t.Error("Accept should refuse a memory that was never quarantined")
	}
	if err := Reject(db, r.ID); err == nil {
		t.Error("Reject should refuse a memory that was never quarantined")
	}
	if err := Accept(db, 999999); err == nil {
		t.Error("Accept should error on an id that does not exist")
	}
}

func TestPendingListsOldestFirst(t *testing.T) {
	db := testDB(t)
	first, _ := Store(db, nil, "", &Memory{Text: "first", Kind: Fact, Source: "mcp", Quarantined: true, Created: 100})
	second, _ := Store(db, nil, "", &Memory{Text: "second", Kind: Fact, Source: "mcp", Quarantined: true, Created: 200})

	pending, err := Pending(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].ID != first.ID || pending[1].ID != second.ID {
		t.Errorf("Pending should return oldest first, got %+v", pending)
	}

	n, err := PendingCount(db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("PendingCount = %d, want 2", n)
	}
}

// Reindexing (Import) must not treat a pending memory as something the user
// deleted from a file it was never written to in the first place.
func TestImportDoesNotReapQuarantinedMemories(t *testing.T) {
	db, dir := store(t)
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Store(db, nil, "", &Memory{
		Text: "an active fact", Kind: Fact, Source: "manual",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Store(db, nil, "", &Memory{
		Text: "an unreviewed fact", Kind: Fact, Source: "mcp", Quarantined: true,
	}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Import(db, nil, "", dir); err != nil {
		t.Fatal(err)
	}

	n, err := Count(db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("row count after reindex = %d, want 2 — Import deleted a pending memory that was never in the file", n)
	}
	pn, err := PendingCount(db)
	if err != nil {
		t.Fatal(err)
	}
	if pn != 1 {
		t.Errorf("PendingCount after reindex = %d, want 1", pn)
	}
}

// The week's accepts are told apart by who accepted: a person, or the review
// switch with nobody watching.
func TestAcceptsAreCountedByHandAndByTheSwitchApart(t *testing.T) {
	db, _ := store(t)
	var ids []int64
	for _, text := range []string{"the drop test is 1.2m", "staging runs on port 9090", "the build needs cgo"} {
		r, err := Store(db, nil, "", &Memory{Text: text, Kind: Fact, Source: "mcp", Project: "kestrel", Quarantined: true})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	if err := Accept(db, ids[0]); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids[1:] {
		if err := AcceptAuto(db, id, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if hand, auto, err := AcceptedSince(db, time.Now().Add(-time.Hour).Unix(), "kestrel"); err != nil || hand != 1 || auto != 2 {
		t.Errorf("AcceptedSince = %d by hand, %d auto, %v; want 1 and 2", hand, auto, err)
	}
	if hand, auto, _ := AcceptedSince(db, time.Now().Add(time.Hour).Unix(), ""); hand+auto != 0 {
		t.Errorf("accepts before the window were counted: %d by hand, %d auto", hand, auto)
	}
	if hand, auto, _ := AcceptedSince(db, 0, "other"); hand+auto != 0 {
		t.Errorf("another project's accepts were counted: %d by hand, %d auto", hand, auto)
	}
}
