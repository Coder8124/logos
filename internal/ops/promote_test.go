package ops

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/session"
)

const cookieDecision = "Sessions use signed cookies, not JWTs, because we need server-side revocation"

// checkpoints commits one checkpoint per list of decisions, oldest first, a
// minute apart and all in the past, so a memory stored after them is newer.
func checkpoints(t *testing.T, db *sql.DB, vault string, decisions ...[]string) {
	t.Helper()
	checkpointsFrom(t, db, vault, time.Now().Add(-time.Hour), decisions...)
}

func checkpointsFrom(t *testing.T, db *sql.DB, vault string, start time.Time, decisions ...[]string) {
	t.Helper()
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	for i, ds := range decisions {
		c := &session.Checkpoint{Project: "kestrel", Agent: "claude-code", Decisions: ds,
			TS: start.Add(time.Duration(i) * time.Minute).Unix()}
		if err := session.Commit(db, vault, c); err != nil {
			t.Fatal(err)
		}
	}
}

func onlyPending(t *testing.T, db *sql.DB) memory.Memory {
	t.Helper()
	ms, err := memory.Pending(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 {
		t.Fatalf("%d proposals queued, want 1: %+v", len(ms), ms)
	}
	return ms[0]
}

func TestADecisionThreeCheckpointsRecordedIsProposedAsProjectMemoryCitingEachOfThem(t *testing.T) {
	db, vault := reviewVault(t, false)
	checkpoints(t, db, vault,
		[]string{cookieDecision},
		[]string{cookieDecision + ".", "Deploys go through staging first"},
		[]string{"sessions use signed cookies,  not JWTs, because we need server-side revocation"},
	)
	got, err := PromoteDecisions(db, vault, "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	if got.Proposed != 1 {
		t.Fatalf("proposed %d, want the one decision that held", got.Proposed)
	}
	m := onlyPending(t, db)
	if m.Kind != memory.Context || m.Project != "kestrel" || m.Source != memory.SourceCheckpoints {
		t.Errorf("proposal is %s/%s/%s, want context/kestrel/%s", m.Kind, m.Project, m.Source, memory.SourceCheckpoints)
	}
	if !strings.HasPrefix(m.Text, "Decision: ") {
		t.Errorf("proposal %q is not worded as the review switch recognises a decision", m.Text)
	}
	if n := strings.Count(m.Turn, "sessions/kestrel/"); n != 3 {
		t.Errorf("proposal cites %d checkpoints, want 3: %q", n, m.Turn)
	}
	if say := PromotionReceipt(got); !strings.Contains(say, "1 decision that held across 3 checkpoints") {
		t.Errorf("the pass does not announce itself: %q", say)
	}
}

func TestADecisionTwoCheckpointsRecordedIsNotProposed(t *testing.T) {
	db, vault := reviewVault(t, false)
	checkpoints(t, db, vault, []string{cookieDecision}, []string{cookieDecision})
	got, err := PromoteDecisions(db, vault, "kestrel")
	if err != nil || got.Proposed != 0 {
		t.Fatalf("proposed %d (%v), want none before the decision has held", got.Proposed, err)
	}
}

func TestADecisionALaterCheckpointReversedIsNotProposed(t *testing.T) {
	db, vault := reviewVault(t, false)
	checkpoints(t, db, vault,
		[]string{cookieDecision}, []string{cookieDecision}, []string{cookieDecision},
		[]string{"Sessions move to JWTs; signed cookies are dropped because revocation is handled by a denylist"},
	)
	got, err := PromoteDecisions(db, vault, "kestrel")
	if err != nil || got.Proposed != 0 {
		t.Fatalf("proposed %d (%v), want none: the project decided the other way since", got.Proposed, err)
	}
}

// The proposal is the queue file's and the decision's checkpoints are
// markdown; the index is a cache. A rebuild must neither lose the proposal
// nor make the next checkpoint propose it a second time.
func TestAProposedDecisionSurvivesDeletingTheIndexAndIsNotProposedAgain(t *testing.T) {
	db, vault := reviewVault(t, false)
	checkpoints(t, db, vault, []string{cookieDecision}, []string{cookieDecision}, []string{cookieDecision})
	if _, err := PromoteDecisions(db, vault, "kestrel"); err != nil {
		t.Fatal(err)
	}

	wiped := openIndexAt(t, vault)
	if _, _, err := memory.Import(wiped, nil, "", vault); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.ImportLog(wiped, vault); err != nil {
		t.Fatal(err)
	}
	if _, _, err := memory.ImportPending(wiped, vault); err != nil {
		t.Fatal(err)
	}
	m := onlyPending(t, wiped)
	if strings.Count(m.Turn, "sessions/kestrel/") != 3 {
		t.Errorf("the rebuilt proposal lost its citations: %q", m.Turn)
	}
	got, err := PromoteDecisions(wiped, vault, "kestrel")
	if err != nil || got.Proposed != 0 {
		t.Fatalf("proposed %d again after the rebuild (%v)", got.Proposed, err)
	}
	onlyPending(t, wiped)
}

func TestARejectedPromotionIsNotProposedAgain(t *testing.T) {
	db, vault := reviewVault(t, false)
	checkpoints(t, db, vault, []string{cookieDecision}, []string{cookieDecision}, []string{cookieDecision})
	if _, err := PromoteDecisions(db, vault, "kestrel"); err != nil {
		t.Fatal(err)
	}
	if err := memory.Reject(db, onlyPending(t, db).ID); err != nil {
		t.Fatal(err)
	}
	checkpointsFrom(t, db, vault, time.Now().Add(-30*time.Minute),
		[]string{cookieDecision + "."}, []string{cookieDecision + "."}, []string{cookieDecision + "."})
	got, err := PromoteDecisions(db, vault, "kestrel")
	if err != nil || got.Proposed != 0 {
		t.Fatalf("proposed %d (%v) after the user rejected it, reworded with a full stop", got.Proposed, err)
	}
}

func TestTheReviewSwitchAcceptsAPromotionWhileItsCheckpointsAreThere(t *testing.T) {
	db, vault := reviewVault(t, true)
	checkpoints(t, db, vault, []string{cookieDecision}, []string{cookieDecision}, []string{cookieDecision})
	if _, err := PromoteDecisions(db, vault, "kestrel"); err != nil {
		t.Fatal(err)
	}
	n, err := AutoReview(db, vault, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("the review switch accepted %d (%v), want the promotion", n, err)
	}

	// The queue file is editable, so what a proposal cites is checked, not
	// trusted: a checkpoint that is not there, a path out of the sessions
	// directory, and a citation on a proposal checkpoints did not make.
	slug := strings.Split(strings.TrimPrefix(onlyAccepted(t, db).Turn, checkpointsRef), ",")[0]
	for _, c := range []struct{ text, source, turn string }{
		{"Decision: deploys freeze on Fridays", memory.SourceCheckpoints, checkpointsRef + "sessions/kestrel/no-such-checkpoint"},
		{"Decision: the queue is drained nightly", memory.SourceCheckpoints, checkpointsRef + "../../etc/passwd"},
		{"Decision: logs rotate weekly", "mcp", checkpointsRef + slug},
	} {
		m := memory.Memory{Text: c.text, Kind: memory.Context, Project: "kestrel", Source: c.source, Quarantined: true, Turn: c.turn}
		if _, err := memory.Store(db, nil, "", &m); err != nil {
			t.Fatal(err)
		}
		if n, _ := AutoReview(db, vault, time.Now()); n != 0 {
			t.Errorf("accepted %q citing %q from source %s", c.text, c.turn, c.source)
		}
	}
}

// onlyAccepted is the one standing checkpoints-sourced memory in kestrel.
func onlyAccepted(t *testing.T, db *sql.DB) memory.Memory {
	t.Helper()
	mems, err := memory.AllInProject(db, "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	var out []memory.Memory
	for _, m := range mems {
		if m.Source == memory.SourceCheckpoints && !m.Quarantined {
			out = append(out, m)
		}
	}
	if len(out) != 1 {
		t.Fatalf("%d promoted memories standing, want 1: %+v", len(out), out)
	}
	return out[0]
}

const jwtReversal = "Sessions move to JWTs; signed cookies are dropped because revocation is handled by a denylist"

// A reversal goes through review like any decision; only once it is standing
// memory does it replace the one it reversed, by Correct, which keeps the old
// memory on record and names it in the new one's supersedes.
func TestAPromotedDecisionIsSupersededOnlyOnceItsReversalHasHeldAndBeenAccepted(t *testing.T) {
	db, vault := reviewVault(t, false)
	checkpoints(t, db, vault, []string{cookieDecision}, []string{cookieDecision}, []string{cookieDecision})
	if _, err := PromoteDecisions(db, vault, "kestrel"); err != nil {
		t.Fatal(err)
	}
	if err := memory.Accept(db, onlyPending(t, db).ID); err != nil {
		t.Fatal(err)
	}
	old := onlyAccepted(t, db)

	later := time.Now().Add(-30 * time.Minute)
	checkpointsFrom(t, db, vault, later, []string{jwtReversal})
	if got, err := PromoteDecisions(db, vault, "kestrel"); err != nil || got.Superseded != 0 || got.Proposed != 0 {
		t.Fatalf("one reversing checkpoint did %+v (%v), want nothing yet", got, err)
	}
	checkpointsFrom(t, db, vault, later.Add(5*time.Minute), []string{jwtReversal}, []string{jwtReversal})
	got, err := PromoteDecisions(db, vault, "kestrel")
	if err != nil || got.Proposed != 1 || got.Superseded != 0 {
		t.Fatalf("a reversal that held did %+v (%v), want it proposed and nothing superseded", got, err)
	}
	if err := memory.Accept(db, onlyPending(t, db).ID); err != nil {
		t.Fatal(err)
	}

	got, err = PromoteDecisions(db, vault, "kestrel")
	if err != nil || got.Superseded != 1 {
		t.Fatalf("superseded %d (%v), want the reversed decision", got.Superseded, err)
	}
	now := onlyAccepted(t, db)
	var link string
	if err := db.QueryRow("SELECT supersedes FROM memories WHERE id = ?", now.ID).Scan(&link); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(now.Text, "Sessions move to JWTs") || link != old.Uid {
		t.Errorf("standing is %q superseding %q, want the reversal superseding %q", now.Text, link, old.Uid)
	}
	if got, _ := PromoteDecisions(db, vault, "kestrel"); got.Superseded != 0 {
		t.Errorf("a second pass superseded %d more", got.Superseded)
	}
}

// Two decisions on one subject that agents copy forward together are both
// standing; neither is a reversal of the other.
func TestTwoRelatedDecisionsRecordedTogetherDoNotSupersedeEachOther(t *testing.T) {
	db, vault := reviewVault(t, false)
	both := []string{cookieDecision, "Session cookies are signed with a key rotated monthly, because revocation alone does not cover a leaked key"}
	checkpoints(t, db, vault, both, both, both)
	got, err := PromoteDecisions(db, vault, "kestrel")
	if err != nil || got.Proposed != 2 {
		t.Fatalf("proposed %d (%v), want both", got.Proposed, err)
	}
	ms, err := memory.Pending(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if err := memory.Accept(db, m.ID); err != nil {
			t.Fatal(err)
		}
	}
	checkpointsFrom(t, db, vault, time.Now().Add(-30*time.Minute), both)
	if got, err := PromoteDecisions(db, vault, "kestrel"); err != nil || got.Superseded != 0 {
		t.Fatalf("superseded %d (%v) between two decisions that stand together", got.Superseded, err)
	}
}
