package main

import (
	"bufio"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/session"
)

// The whole loop: a ruling and a procedure about a file that moved are
// offered, one is retired and one re-confirmed, and neither is offered again —
// because both answers are in the vault. The ruling is longer than the record
// cap, so the retire is keyed on the whole entry and not on the capped one.
func TestReviewStaleOffersWhatDriftedAndItsAnswersStick(t *testing.T) {
	repo, vault, db := editRepo(t)
	if err := memory.Init(db); err != nil {
		t.Fatal(err)
	}
	memory.SetVault(db, vault)
	t.Cleanup(func() { memory.SetVault(db, "") })
	_, anchor := gitstate.Head(repo)

	ruling := "streaming the export — internal/parse/reader.go loads the whole file. " + strings.Repeat("measured at 2GB and 4GB. ", 60)
	if err := session.Commit(db, vault, &session.Checkpoint{
		Project: "api", Agent: "claude", Task: "import", Next: "split it",
		Git: gitstate.State{Commit: anchor}, Failed: []string{ruling},
	}); err != nil {
		t.Fatal(err)
	}
	proc := memory.Memory{
		Text: "route: read internal/parse/reader.go before touching the sink | trap: the sink assumes the whole file is loaded",
		Kind: memory.Procedure, Source: "manual", Project: "api", Commit: anchor,
	}
	if _, err := memory.Store(db, nil, "", &proc); err != nil {
		t.Fatal(err)
	}
	commitTo(t, repo, "internal/parse/reader.go", "read in chunks\n")
	_, head := gitstate.Head(repo)

	items, err := gatherStale(vault, db, "api", repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want the ruling and the procedure offered, got %d", len(items))
	}
	var answers strings.Builder
	for _, it := range items {
		if it.mem != nil {
			answers.WriteString("c\n")
		} else {
			answers.WriteString("r\n")
		}
	}
	kept, retired, confirmed, err := reviewStale(bufio.NewReader(strings.NewReader(answers.String())), items, vault, db, "api", head)
	if err != nil {
		t.Fatal(err)
	}
	if kept != 0 || retired != 1 || confirmed != 1 {
		t.Fatalf("got %d kept, %d retired, %d confirmed", kept, retired, confirmed)
	}

	again, err := gatherStale(vault, db, "api", repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("answered items were offered again: %+v", again)
	}
}
