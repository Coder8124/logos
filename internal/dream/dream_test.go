package dream

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/router"
	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // the single-connection discipline the whole app runs on
	t.Cleanup(func() { db.Close() })
	if err := memory.Init(db); err != nil {
		t.Fatal(err)
	}
	if err := InitQueue(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestInsightValidate(t *testing.T) {
	cases := []struct {
		name string
		in   Insight
		ok   bool
	}{
		{"good", Insight{Text: "x", EndpointA: 1, EndpointB: 2, Conf: 0.5}, true},
		{"no text", Insight{EndpointA: 1, EndpointB: 2, Conf: 0.5}, false},
		{"missing endpoint", Insight{Text: "x", EndpointA: 1, Conf: 0.5}, false},
		{"self reference", Insight{Text: "x", EndpointA: 1, EndpointB: 1, Conf: 0.5}, false},
		{"bad confidence", Insight{Text: "x", EndpointA: 1, EndpointB: 2, Conf: 2}, false},
	}
	for _, c := range cases {
		if err := c.in.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate() err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

// The grounding guard must be structural: an ungrounded insight can never reach
// the queue, no matter what calls Enqueue.
func TestEnqueueRejectsUngrounded(t *testing.T) {
	db := testDB(t)
	if err := Enqueue(db, &Insight{Text: "a floating idea", Conf: 0.5}); err == nil {
		t.Fatal("expected Enqueue to reject an insight with no endpoints")
	}
	if n, _ := PendingCount(db); n != 0 {
		t.Fatalf("ungrounded insight was queued: %d pending", n)
	}
}

func TestEnqueueListGet(t *testing.T) {
	db := testDB(t)
	in := &Insight{Kind: Connection, Text: "A relates to B", EndpointA: 1, EndpointB: 2, Conf: 0.5}
	if err := Enqueue(db, in); err != nil {
		t.Fatal(err)
	}
	got, err := List(db, Pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "A relates to B" {
		t.Fatalf("List = %+v", got)
	}
	one, err := Get(db, in.ID)
	if err != nil || one.EndpointA != 1 || one.EndpointB != 2 {
		t.Fatalf("Get = %+v, err=%v", one, err)
	}
}

// Accepting an insight stores a dream-sourced memory at low confidence and marks
// the insight accepted rather than leaving it pending. Runs without a provider:
// with nil, Store skips embedding but still records the fact.
func TestAcceptStoresLowConfidenceMemory(t *testing.T) {
	db := testDB(t)
	in := Insight{Kind: Connection, Text: "logos's retrieval could help another project", EndpointA: 1, EndpointB: 2, Conf: 0.5}
	if err := Enqueue(db, &in); err != nil {
		t.Fatal(err)
	}
	stored, err := Accept(db, nil, "", in)
	if err != nil {
		t.Fatal(err)
	}
	if !stored {
		t.Fatal("Accept did not store a memory")
	}
	var conf float64
	var source string
	if err := db.QueryRow(`SELECT confidence, source FROM memories WHERE text = ?`, in.Text).Scan(&conf, &source); err != nil {
		t.Fatal(err)
	}
	if source != "dream" || conf != 0.5 {
		t.Fatalf("accepted memory source=%q conf=%v, want dream/0.5", source, conf)
	}
	if n, _ := PendingCount(db); n != 0 {
		t.Fatalf("insight still pending after accept: %d", n)
	}
}

// A memory in the review queue has not been accepted, so it has not earned an
// opinion about its own importance yet; it is not counted as faded.
func TestAMemoryWaitingForReviewIsNotCountedAsFaded(t *testing.T) {
	db := testDB(t)
	db.Exec(`INSERT INTO memories (text, kind, salience, confidence, source, created, quarantined) VALUES ('held','context',1.0,0.7,'manual',1,1)`)
	var res Result
	if err := nrem(db, nil, false, &res); err != nil {
		t.Fatal(err)
	}
	if res.Faded != 0 {
		t.Errorf("counted %d faded, want 0 — the only memory is waiting for review", res.Faded)
	}
}

// ---------- a night that fails says so ----------

// Consolidation used to be called as `if err == nil { record it }`, so a broken
// memory store printed "0 consolidated (0 merged, 0 superseded)" — the same line
// a quiet, healthy night prints. The worst outcome available in this codebase is
// an operation that failed and returned a success-shaped result.
func TestAFailedConsolidationStopsTheNightInsteadOfReportingZero(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec("DROP TABLE memories"); err != nil {
		t.Fatal(err)
	}

	var res Result
	err := nrem(db, nil, false, &res)
	if err == nil {
		t.Fatal("a broken memory store reported a night with nothing to consolidate")
	}
	if res.Replayed != 0 || res.Merged != 0 {
		t.Errorf("a failed replay reported counts: %+v", res)
	}
}

// Not having a model is a condition, not a failure — replay needs one to judge
// whether two memories say the same thing, and a machine without one must still
// get its fading count rather than an error.
func TestNoModelSkipsReplayRatherThanFailingTheNight(t *testing.T) {
	db := testDB(t)
	db.Exec(`INSERT INTO memories (text, kind, salience, confidence, source, created) VALUES ('a','context',0.5,0.7,'manual',1)`)

	rt, err := router.New(&router.Config{Tiers: map[string]router.TierConfig{
		"t0": {Model: "model-that-is-not-installed"},
		"t1": {Model: "model-that-is-not-installed"},
		"t2": {Model: "model-that-is-not-installed"},
	}}, t.TempDir())
	if errors.Is(err, router.ErrNoRuntime) {
		t.Skip("no local model runtime on this machine")
	}
	if err != nil {
		t.Fatal(err)
	}

	var res Result
	if err := nrem(db, rt, false, &res); err != nil {
		t.Fatalf("a machine with no usable model could not dream: %v", err)
	}
	if !res.ReplaySkipped {
		t.Error("replay was reported as having run with no model to run it")
	}
	if res.Faded == 0 {
		t.Error("the model-free part of the pass did not run")
	}
}

// nrem with a nil router (no local runtime at all) must still complete the
// model-free half of the pass rather than panic reaching for rt.Local().
func TestNremWithNilRouterStillCountsWhatFaded(t *testing.T) {
	db := testDB(t)
	db.Exec(`INSERT INTO memories (text, kind, salience, confidence, source, created) VALUES ('a','context',0.5,0.7,'manual',1)`)

	var res Result
	if err := nrem(db, nil, false, &res); err != nil {
		t.Fatalf("nrem with no model runtime must still run: %v", err)
	}
	if !res.ReplaySkipped {
		t.Error("replay was reported as having run with no runtime")
	}
	if res.Faded == 0 {
		t.Error("the model-free part of the pass did not run with a nil router")
	}
}

func TestRunAcceptsTimeAndPhaseWithoutAmbientEvents(t *testing.T) {
	db := testDB(t)
	db.Exec(`INSERT INTO memories (text, kind, salience, confidence, source, created) VALUES ('a','context',0.5,0.7,'manual',1)`)

	res, err := Run(db, t.TempDir(), nil, time.Now(), PhaseNREM, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Date == "" {
		t.Error("Run did not stamp a date on the result")
	}
	if res.Faded == 0 {
		t.Error("NREM did not run")
	}
}

// The nightly downscale multiplied every stored salience by 0.98 in the index
// and wrote none of it to the kind files, so `logos index` put back whatever
// the files last said and ranking depended on whether the cache had survived
// (#136, invariant 1). Disuse is already applied where salience is read, by
// EffectiveSalience; the night now counts what has faded and stores nothing.
func TestADreamLeavesEveryStoredSalienceAsItWas(t *testing.T) {
	db := testDB(t)
	db.Exec(`INSERT INTO memories (text, kind, salience, confidence, source, created) VALUES ('old','context',0.8,0.7,'manual',1)`)

	var res Result
	if err := nrem(db, nil, false, &res); err != nil {
		t.Fatal(err)
	}
	var s float64
	db.QueryRow(`SELECT salience FROM memories WHERE text='old'`).Scan(&s)
	if s != 0.8 {
		t.Errorf("a dream changed stored salience 0.8 -> %v, which no kind file carries", s)
	}
}
