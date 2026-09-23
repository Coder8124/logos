package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/contextpack"
)

// The ledger is the running total, so it has to be read back from the vault
// alone: nothing about it may live in .logos/index.db, which the product
// promises can be deleted without losing anything.
func TestTheLedgerIsReadBackFromTheVaultAlone(t *testing.T) {
	vault := t.TempDir()
	b := contextpack.Budget{Spent: 300, Overhead: 50, Candidates: 1200}
	if err := RecordPack(vault, "mcp:resume", "kestrel", b); err != nil {
		t.Fatal(err)
	}
	if err := Record(vault, Event{Kind: KindDeadEnd, Via: "cli:tried", Project: "kestrel", Rulings: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(vault, ".logos")); !os.IsNotExist(err) {
		t.Errorf("recording usage touched the index directory (stat err %v)", err)
	}

	events, bad, err := Read(vault)
	if err != nil || bad != 0 {
		t.Fatalf("Read = %d events, %d bad, %v", len(events), bad, err)
	}
	got := Sum(events, "")
	if got.Packs != 1 || got.Sent != 350 || got.Full != 1250 || got.Saved() != 900 {
		t.Errorf("pack totals = %+v, want 1 pack, 350 sent, 1250 uncut, 900 saved", got)
	}
	if got.DeadEndChecks != 1 || got.Rulings != 2 {
		t.Errorf("dead-end totals = %+v, want 1 check returning 2", got)
	}
}

// A total for one project must not borrow another's, or the number a user
// quotes for this repository is partly work done somewhere else.
func TestATotalForOneProjectCountsOnlyThatProject(t *testing.T) {
	events := []Event{
		{TS: 100, Kind: KindPack, Project: "kestrel", Sent: 10, Full: 40},
		{TS: 200, Kind: KindPack, Project: "heron", Sent: 10, Full: 90},
		{TS: 300, Kind: KindDeadEnd, Project: "heron", Rulings: 1},
	}
	k := Sum(events, "kestrel")
	if k.Packs != 1 || k.Saved() != 30 || k.DeadEndChecks != 0 || k.Since != 100 {
		t.Errorf("kestrel = %+v, want only its own pack", k)
	}
	if all := Sum(events, ""); all.Packs != 2 || all.Saved() != 110 || all.DeadEndChecks != 1 {
		t.Errorf("all projects = %+v", all)
	}
}

// A torn line — a machine that lost power mid-write — costs that line and not
// the month, but it is counted: a total silently missing lines is a total that
// is wrong without saying so.
func TestATornLineIsCountedRatherThanSilentlyDropped(t *testing.T) {
	vault := t.TempDir()
	if err := Record(vault, Event{TS: time.Now().Unix(), Kind: KindPack, Via: "cli:resume", Sent: 5, Full: 9}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(vault, Dir, time.Now().Format("2006-01")+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"ts":1,"kind":"pa` + "\n")
	f.Close()

	events, bad, err := Read(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || bad != 1 {
		t.Errorf("Read = %d events and %d unreadable, want 1 and 1", len(events), bad)
	}
}

// Packs were recorded under the bare project, dead ends under the worktree
// scope, and `tried --project` under whatever was typed — so one project's
// events sat under three keys and `logos usage kestrel` counted one of them.
func TestOneProjectIsCountedUnderOneNameHoweverItWasSpelled(t *testing.T) {
	vault := t.TempDir()
	for _, p := range []string{"kestrel", "kestrel/feature-a", "/Users/x/code/kestrel", "./kestrel"} {
		if err := Record(vault, Event{Kind: KindDeadEnd, Project: p, Rulings: 1}); err != nil {
			t.Fatal(err)
		}
	}
	events, _, err := Read(vault)
	if err != nil {
		t.Fatal(err)
	}
	if got := Sum(events, "kestrel").DeadEndChecks; got != 4 {
		t.Errorf("counted %d of kestrel's 4 checks", got)
	}
	if got := Sum(events, "~/code/kestrel").DeadEndChecks; got != 4 {
		t.Errorf("asking by path counted %d of kestrel's 4 checks", got)
	}
}
