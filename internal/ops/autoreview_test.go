package ops

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/transcript"

	_ "modernc.org/sqlite"
)

// reviewVault is a vault with a fresh index over it and one transcript, three
// turns long, that every live citation in these tests points into.
func reviewVault(t *testing.T, on bool) (*sql.DB, string) {
	t.Helper()
	vault := t.TempDir()
	db := openIndexAt(t, vault)
	if on {
		if err := auto.Set(vault, string(auto.Review), true); err != nil {
			t.Fatal(err)
		}
	}
	old := readTranscript
	readTranscript = func(harness, path string) (*transcript.Session, error) {
		if path != "/t/live.jsonl" {
			return nil, errors.New("no such transcript")
		}
		return &transcript.Session{Path: path, Turns: make([]transcript.Turn, 3)}, nil
	}
	t.Cleanup(func() { readTranscript = old })
	return db, vault
}

func openIndexAt(t *testing.T, vault string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := memory.Init(db); err != nil {
		t.Fatal(err)
	}
	memory.SetVault(db, vault)
	t.Cleanup(func() { memory.SetVault(db, ""); db.Close() })
	return db
}

var liveTurn = TurnRef("claude-code", "/t/live.jsonl", 2)

func propose(t *testing.T, db *sql.DB, text string, kind memory.Kind, turn string) int64 {
	t.Helper()
	m := memory.Memory{Text: text, Kind: kind, Source: "mcp", Agent: "claude-code", Quarantined: true, Turn: turn}
	r, err := memory.Store(db, nil, "", &m)
	if err != nil {
		t.Fatal(err)
	}
	return r.ID
}

func pendingTexts(t *testing.T, db *sql.DB) []string {
	t.Helper()
	ms, err := memory.Pending(db)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range ms {
		out = append(out, m.Text)
	}
	return out
}

func TestAutoModeOffLeavesEveryCandidateInTheQueue(t *testing.T) {
	db, vault := reviewVault(t, false)
	propose(t, db, "Decided: the billing service owns refunds", memory.Fact, liveTurn)

	n, err := AutoReview(db, vault, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(pendingTexts(t, db)) != 1 {
		t.Fatalf("the switch is off and it accepted %d; queue is %v", n, pendingTexts(t, db))
	}
}

// Each rule on its own: one candidate that passes everything, and beside it
// one that fails exactly one rule and so must still be waiting.
func TestOnlyACitedUnredactedDecisionOrTrappedProcedureIsAccepted(t *testing.T) {
	db, vault := reviewVault(t, true)
	propose(t, db, "Decided: the billing service owns refunds", memory.Fact, liveTurn)
	propose(t, db, "route: run migrations with --lock | trap: two deploys race the schema", memory.Procedure, liveTurn)
	waits := []string{
		propText(t, db, "Decided: invoices are archived after a year", memory.Fact, ""),                                        // no citation
		propText(t, db, "Decided: exports go to the reports bucket", memory.Fact, TurnRef("claude-code", "/t/gone.jsonl", 1)),  // transcript gone
		propText(t, db, "Decided: retries back off exponentially", memory.Fact, TurnRef("claude-code", "/t/live.jsonl", 9)),    // turn gone
		propText(t, db, "The staging cluster runs in eu-west-1", memory.Fact, liveTurn),                                        // not a decision
		propText(t, db, "Decided: the deploy key is sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", memory.Fact, liveTurn), // redacted
	}

	n, err := AutoReview(db, vault, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("accepted %d, want the decision and the procedure", n)
	}
	got := strings.Join(pendingTexts(t, db), "\n")
	for _, w := range waits {
		if !strings.Contains(got, w[:20]) {
			t.Errorf("%q was accepted; it should still wait. Queue:\n%s", w, got)
		}
	}
}

func propText(t *testing.T, db *sql.DB, text string, kind memory.Kind, turn string) string {
	propose(t, db, text, kind, turn)
	return text
}

// Queued before the pin existed, so the check made when it arrived could not
// have seen the dispute. The sweep has to ask again.
func TestACandidateThatContradictsAPinIsNeverAutoAccepted(t *testing.T) {
	db, vault := reviewVault(t, true)
	propose(t, db, "Decided: the primary database is MySQL", memory.Fact, liveTurn)
	pinned := memory.Memory{Text: "Decided: the primary database is Postgres", Kind: memory.Fact, Source: "cli"}
	r, err := memory.Store(db, nil, "", &pinned)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Pin(db, r.ID); err != nil {
		t.Fatal(err)
	}

	n, err := AutoReview(db, vault, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("accepted a memory that contradicts a pinned one; queue is %v", pendingTexts(t, db))
	}
}

func TestAnAutoAcceptedMemorySurvivesDeletingTheIndexStillMarkedAuto(t *testing.T) {
	db, vault := reviewVault(t, true)
	id := propose(t, db, "Decided: the billing service owns refunds", memory.Fact, liveTurn)
	if n, err := AutoReview(db, vault, time.Now()); err != nil || n != 1 {
		t.Fatalf("accepted %d, %v", n, err)
	}

	wiped := openIndexAt(t, vault)
	if _, _, err := memory.Import(wiped, nil, "", vault); err != nil {
		t.Fatal(err)
	}
	ms, err := memory.AutoAccepted(wiped, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].ID != id || ms[0].AutoAccepted == 0 {
		t.Fatalf("after the rebuild the auto-accepted list is %+v, want #%d marked auto", ms, id)
	}
	// And so it can still be handed back after the rebuild.
	if err := memory.UndoAuto(wiped, id); err != nil {
		t.Fatalf("could not undo after a rebuild: %v", err)
	}
}

// A memory the user handed back is theirs to decide. The next resume must not
// take it off them again.
func TestAnUndoneMemoryIsNotAcceptedAgainByTheNextSweep(t *testing.T) {
	db, vault := reviewVault(t, true)
	id := propose(t, db, "Decided: the billing service owns refunds", memory.Fact, liveTurn)
	if _, err := AutoReview(db, vault, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := memory.UndoAuto(db, id); err != nil {
		t.Fatal(err)
	}
	if got := pendingTexts(t, db); len(got) != 1 {
		t.Fatalf("undo left the queue as %v", got)
	}
	if n, err := AutoReview(db, vault, time.Now()); err != nil || n != 0 {
		t.Fatalf("the next sweep accepted %d again (%v)", n, err)
	}
	// Nor after a rebuild: the cleared citation is in the queue file too.
	wiped := openIndexAt(t, vault)
	if _, _, err := memory.ImportPending(wiped, vault); err != nil {
		t.Fatal(err)
	}
	if n, err := AutoReview(wiped, vault, time.Now()); err != nil || n != 0 {
		t.Fatalf("after a rebuild the sweep accepted %d again (%v)", n, err)
	}
}

func TestResumeSaysHowManyWereAcceptedAutomatically(t *testing.T) {
	db, vault := reviewVault(t, true)
	propose(t, db, "Decided: the billing service owns refunds", memory.Fact, liveTurn)
	propose(t, db, "The staging cluster runs in eu-west-1", memory.Fact, liveTurn)

	lead := RunAutoReview(db, vault, "shop") + AutoLead(vault, "shop")
	if !strings.Contains(lead, "accepted 1 memory from the review queue") {
		t.Errorf("resume did not say what the switch accepted:\n%s", lead)
	}
	queue := ReviewQueue(db, "logos").Text()
	if !strings.Contains(queue, "accepted 1 automatically since your last review, 1 waiting for you") {
		t.Errorf("the queue line does not count what was accepted for the user:\n%s", queue)
	}
	if _, err := os.Stat(auto.LogPath(vault)); err != nil {
		t.Errorf("the sweep was not logged: %v", err)
	}
}

func TestATurnReferenceKeepsAPathWithAColon(t *testing.T) {
	h, p, n, ok := parseTurnRef(TurnRef("codex", `C:\Users\a\s.jsonl`, 4))
	if !ok || h != "codex" || p != `C:\Users\a\s.jsonl` || n != 4 {
		t.Fatalf("got %q %q %d %v", h, p, n, ok)
	}
}

// The citation is what makes a proposal eligible, and the queue file is what
// a rebuild reads the queue back from.
func TestATurnCitationSurvivesDeletingTheIndex(t *testing.T) {
	db, vault := reviewVault(t, true)
	propose(t, db, "Decided: the billing service owns refunds", memory.Fact, liveTurn)

	wiped := openIndexAt(t, vault)
	if _, _, err := memory.ImportPending(wiped, vault); err != nil {
		t.Fatal(err)
	}
	if n, err := AutoReview(wiped, vault, time.Now()); err != nil || n != 1 {
		t.Fatalf("after a rebuild the sweep accepted %d (%v), want the cited decision", n, err)
	}
}

// A line the user deleted from the queue is logged as rejected when a sweep
// notices it, which is after the sweep read the clock. Accepts stamped with
// that earlier time fell before "your last review", and vanished from every
// list that counts what the switch did since.
func TestASweepThatNoticesAHandRejectionStillCountsItsAcceptsAsUnreviewed(t *testing.T) {
	db, vault := reviewVault(t, true)
	propose(t, db, "Decided: the billing service owns refunds", memory.Fact, liveTurn)
	propose(t, db, "The staging cluster runs in eu-west-1", memory.Fact, liveTurn)
	path := filepath.Join(vault, memory.Dir, memory.PendingFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, l := range strings.Split(string(raw), "\n") {
		if !strings.Contains(l, "eu-west-1") {
			kept = append(kept, l)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	// Read a few seconds before the reconcile, as a sweep that reads
	// transcripts first can.
	if n, err := AutoReview(db, vault, time.Now().Add(-3*time.Second)); err != nil || n != 1 {
		t.Fatalf("accepted %d (%v), want the decision", n, err)
	}
	last, err := memory.LastReview(db)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := memory.AutoAccepted(db, last)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 {
		t.Fatalf("the accept is not counted since the last review (at %d): %+v", last, ms)
	}
}
