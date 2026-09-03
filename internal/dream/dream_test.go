package dream

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/router"
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
	in := Insight{Kind: Connection, Text: "brain's retrieval could help another project", EndpointA: 1, EndpointB: 2, Conf: 0.5}
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

func TestDownscaleFloorsAndRenormalises(t *testing.T) {
	db := testDB(t)
	db.Exec(`INSERT INTO memories (text, kind, salience, confidence, source, created) VALUES ('high','context',1.0,0.7,'manual',1)`)
	db.Exec(`INSERT INTO memories (text, kind, salience, confidence, source, created) VALUES ('floor','context',0.05,0.7,'manual',1)`)

	n, err := downscale(db, false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("downscale touched %d rows, want 1 (only the one above the floor)", n)
	}

	var hi, lo float64
	db.QueryRow(`SELECT salience FROM memories WHERE text='high'`).Scan(&hi)
	db.QueryRow(`SELECT salience FROM memories WHERE text='floor'`).Scan(&lo)
	if hi >= 1.0 || hi < 0.9 {
		t.Fatalf("high salience = %v, want gently reduced from 1.0", hi)
	}
	if lo != 0.05 {
		t.Fatalf("floored salience = %v, want left at the floor 0.05", lo)
	}
}

func TestDownscaleDryRunWritesNothing(t *testing.T) {
	db := testDB(t)
	db.Exec(`INSERT INTO memories (text, kind, salience, confidence, source, created) VALUES ('m','context',1.0,0.7,'manual',1)`)
	if _, err := downscale(db, true); err != nil {
		t.Fatal(err)
	}
	var s float64
	db.QueryRow(`SELECT salience FROM memories WHERE text='m'`).Scan(&s)
	if s != 1.0 {
		t.Fatalf("dry-run downscale changed salience to %v", s)
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
	err := nrem(db, nil, "", nil, time.Now(), false, &res)
	if err == nil {
		t.Fatal("a broken memory store reported a night with nothing to consolidate")
	}
	if res.Replayed != 0 || res.Merged != 0 {
		t.Errorf("a failed replay reported counts: %+v", res)
	}
}

// The same swallow on the gist path: a gist that cannot be written is not a
// gist that was already known.
func TestAGistThatCannotBeStoredIsNotCountedAsAlreadyKnown(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec("DROP TABLE memories"); err != nil {
		t.Fatal(err)
	}

	learned, err := storeGists(db, nil, "", []string{"Usually opens Ghostty around 09:00 on weekdays."})
	if err == nil {
		t.Fatal("a gist that could not be stored was reported as stored")
	}
	if learned != 0 {
		t.Errorf("learned = %d after a failed write, want 0", learned)
	}
}

// Not having a model is a condition, not a failure — replay needs one to judge
// whether two memories say the same thing, and a machine without one must still
// get its downscaling pass rather than an error.
func TestNoModelSkipsReplayRatherThanFailingTheNight(t *testing.T) {
	db := testDB(t)
	if err := capture.InitStore(db); err != nil {
		t.Fatal(err)
	}
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
	if err := nrem(db, rt, "", nil, time.Now(), false, &res); err != nil {
		t.Fatalf("a machine with no usable model could not dream: %v", err)
	}
	if !res.ReplaySkipped {
		t.Error("replay was reported as having run with no model to run it")
	}
	if res.Downscaled == 0 {
		t.Error("the model-free part of the pass did not run")
	}
}
