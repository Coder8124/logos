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
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Hour)
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
	checkpoints(t, db, vault, []string{cookieDecision})
	got, err := PromoteDecisions(db, vault, "kestrel")
	if err != nil || got.Proposed != 0 {
		t.Fatalf("proposed %d (%v) after the user rejected it", got.Proposed, err)
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

	gone := propose(t, db, "Decision: deploys freeze on Fridays", memory.Context, checkpointsRef+"sessions/kestrel/no-such-checkpoint")
	if n, _ := AutoReview(db, vault, time.Now()); n != 0 {
		t.Errorf("accepted a promotion citing a checkpoint that is not there (#%d)", gone)
	}
}

func TestAPromotedDecisionALaterCheckpointReversesIsSupersededNotEdited(t *testing.T) {
	db, vault := reviewVault(t, false)
	checkpoints(t, db, vault, []string{cookieDecision}, []string{cookieDecision}, []string{cookieDecision})
	if _, err := PromoteDecisions(db, vault, "kestrel"); err != nil {
		t.Fatal(err)
	}
	old := onlyPending(t, db)
	if err := memory.Accept(db, old.ID); err != nil {
		t.Fatal(err)
	}

	reversal := "Sessions move to JWTs; signed cookies are dropped because revocation is handled by a denylist"
	if err := session.Commit(db, vault, &session.Checkpoint{Project: "kestrel", Agent: "codex",
		Decisions: []string{reversal}, TS: time.Now().Add(time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	got, err := PromoteDecisions(db, vault, "kestrel")
	if err != nil || got.Superseded != 1 {
		t.Fatalf("superseded %d (%v), want the reversed decision", got.Superseded, err)
	}
	mems, err := memory.AllInProject(db, "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	var now *memory.Memory
	for i, m := range mems {
		if m.ID == old.ID {
			t.Errorf("the reversed decision is still standing: %q", m.Text)
		}
		if strings.Contains(m.Text, "Sessions move to JWTs") {
			now = &mems[i]
		}
	}
	if now == nil || now.Source != memory.SourceCheckpoints {
		t.Fatalf("no promoted memory holds the reversal: %+v", mems)
	}
	if got, _ := PromoteDecisions(db, vault, "kestrel"); got.Superseded != 0 {
		t.Errorf("a second pass superseded %d more", got.Superseded)
	}
}
