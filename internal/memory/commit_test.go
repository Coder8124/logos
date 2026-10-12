package memory

import "testing"

// The commit a memory was written at is what recall measures its named files
// against. Held only in the index, deleting the index would quietly turn every
// anchored procedure back into an unanchored one that is never flagged.
func TestTheMemoryCommitSurvivesDeletingTheIndex(t *testing.T) {
	db, dir := vaultDB(t)
	m := Memory{Text: "route: run go generate before build | trap: gen.go goes stale", Kind: Procedure, Source: "manual", Commit: "5028439a1b2c"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	wiped := testDB(t)
	SetVault(wiped, dir)
	t.Cleanup(func() { SetVault(wiped, "") })
	if _, _, err := Import(wiped, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	mems, err := All(wiped)
	if err != nil {
		t.Fatal(err)
	}
	if len(mems) != 1 || mems[0].Commit != "5028439a1b2c" {
		t.Fatalf("after the rebuild the vault gave back %+v, want the procedure with its commit", mems)
	}
}

// A line someone typed into the file is data, and so is its commit field: only
// a hex sha is kept, so nothing else can reach git as a revision.
func TestAHandTypedCommitThatIsNotAShaIsDropped(t *testing.T) {
	var m Memory
	applyMeta(&m, "<!-- logos id=3 commit=--output=/tmp/x -->")
	if m.Commit != "" {
		t.Fatalf("commit = %q, want it dropped", m.Commit)
	}
	applyMeta(&m, "<!-- logos id=3 commit=5028439 -->")
	if m.Commit != "5028439" {
		t.Fatalf("commit = %q, want 5028439", m.Commit)
	}
}
