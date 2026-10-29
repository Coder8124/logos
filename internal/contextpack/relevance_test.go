package contextpack

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/session"
)

const (
	oldWaveguide = "re-quoting the waveguide supplier for a lower price — no movement"
	oldTailwind  = "switching the landing page to tailwind — broke the build"
)

// oldCheckpoint is the one seeded behind the four recent ones.
type oldCheckpoint struct {
	age    time.Duration
	failed []string
	git    gitstate.State
}

// seedBehindHistory commits each old checkpoint, then four recent ones on the
// same work — so every old one is past History's reach — and indexes it all.
func seedBehindHistory(t *testing.T, ix *index.Index, now time.Time, git gitstate.State, olds ...oldCheckpoint) {
	t.Helper()
	dir := filepath.Join(ix.Vault, session.CheckpointDir, "kestrel-one", session.ConvosDir, "_unattached")
	for i, o := range olds {
		c := &session.Checkpoint{Project: "kestrel-one", Agent: "cursor", Task: "price the optics", Failed: o.failed,
			TS: now.Add(-o.age).Unix(), Git: o.git}
		if err := session.Commit(ix.DB, ix.Vault, c); err != nil {
			t.Fatal(err)
		}
		// Commit names a file by the second it ran, and checkpoints are
		// ordered by name, so each old one is given the name it would have had
		// at its age; otherwise a fast machine names them all in one second
		// and the order is luck.
		from := filepath.Join(dir, filepath.Base(c.Slug)+".md")
		to := filepath.Join(dir, fmt.Sprintf("%s-cursor%d.md", now.Add(-o.age).Format("20060102-150405"), i))
		if err := os.Rename(from, to); err != nil {
			t.Fatal(err)
		}
	}
	for i := 4; i >= 1; i-- {
		if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{Project: "kestrel-one", Agent: "claude",
			Task:  "lower the waveguide cost",
			State: fmt.Sprintf("pass %d over the optics costs", i), Git: git,
			Next: "get a second waveguide supplier quote", TS: now.Add(-time.Duration(i) * time.Hour).Unix()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
}

func resumeKestrel(t *testing.T, ix *index.Index, embed *provider.Provider, model string, now time.Time, since Since, dir string) (Pack, string) {
	t.Helper()
	p, err := Build(ix, embed, model, Request{Task: "resume work on kestrel-one", Hint: "kestrel-one", Since: since, Dir: dir, Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return p, p.Render()
}

// resumeAfterAnOldRuling seeds a project whose relevant dead end sits in a
// checkpoint seven weeks old, behind four recent ones — past both the week
// window and History's reach — and resumes it the way the CLI does.
func resumeAfterAnOldRuling(t *testing.T) (Pack, string) {
	t.Helper()
	ix := seedVault(t)
	now := time.Now()
	seedBehindHistory(t, ix, now, gitstate.State{}, oldCheckpoint{age: 50 * 24 * time.Hour, failed: []string{oldWaveguide, oldTailwind}})
	return resumeKestrel(t, ix, nil, "", now, SinceWeek, "")
}

// A week window and a four-checkpoint history hid the one dead end that was
// about exactly the work being resumed, because it was recorded seven weeks
// ago. The task reaches it, so it comes back; the unrelated one beside it in
// the same checkpoint does not.
func TestResumeSurfacesAnOutOfWindowCheckpointWhenItScoresRelevantToTheTask(t *testing.T) {
	p, out := resumeAfterAnOldRuling(t)
	if len(p.Revived) != 1 || p.Revived[0].Text != oldWaveguide {
		t.Fatalf("revived = %+v, want only the waveguide ruling\n%s", p.Revived, out)
	}
	if !strings.Contains(out, "re-quoting the waveguide supplier") {
		t.Errorf("the relevant old ruling is not in the pack:\n%s", out)
	}
	if strings.Contains(out, "tailwind") {
		t.Errorf("an old ruling that shares nothing with the task came back too:\n%s", out)
	}
}

// A ruling from seven weeks ago read as this week's tells an agent the dead end
// is fresh. It carries its age and the words "outside the window", under its
// own heading rather than the current checkpoint's.
func TestAnOutOfWindowCheckpointIsLabelledDistinctlyFromTheNormalWindow(t *testing.T) {
	_, out := resumeAfterAnOldRuling(t)
	head := strings.Index(out, "**Ruled out earlier, and matching this task:**")
	if head < 0 {
		t.Fatalf("no heading of its own for the revived ruling:\n%s", out)
	}
	if !strings.Contains(out[head:], "- (cursor, 1mo ago, outside the window) re-quoting the waveguide") {
		t.Errorf("the revived ruling is not labelled with its author, age and window:\n%s", out)
	}
	if tried := strings.Index(out, "**Already tried, didn't work:**"); tried >= 0 && strings.Index(out[tried:], "waveguide supplier") < head-tried {
		t.Errorf("the old ruling is listed as the latest checkpoint's:\n%s", out)
	}
}

// The window line counted what the filter set aside and then said everything
// fell inside it — beside a ruling that plainly did not. What came back past
// the window is stated apart from what stayed out.
func TestTheSetAsideCountExcludesItemsSurfacedByRelevance(t *testing.T) {
	p, out := resumeAfterAnOldRuling(t)
	if p.OutOfWindow != 0 {
		t.Errorf("OutOfWindow = %d, want 0: nothing the window holds was set aside", p.OutOfWindow)
	}
	if strings.Contains(out, "Everything recorded falls inside it") {
		t.Errorf("the window line says nothing is outside it, above a ruling from outside it:\n%s", out)
	}
	if strings.Contains(out, "outside that period") {
		t.Errorf("the revived ruling is counted as set aside:\n%s", out)
	}
	if !strings.Contains(out, "1 ruled-out approach from older checkpoints is listed because it matches this task") {
		t.Errorf("the window line does not account for the revived ruling:\n%s", out)
	}
}

// The SessionStart hook resumes with LOGOS_EMBED=off, so relevance there is
// keyword matching. A reader who assumes a model looked trusts a miss the
// keywords cannot back, so the pack says how it matched.
func TestWithEmbeddingsOffResumeSaysRelevanceIsLexicalOrOff(t *testing.T) {
	p, out := resumeAfterAnOldRuling(t)
	if !p.ReviveLexical {
		t.Error("a pack built with no embedding model did not record that relevance was lexical")
	}
	if !strings.Contains(out, "by keyword only (no embedding model)") {
		t.Errorf("the pack does not say relevance was keyword-only:\n%s", out)
	}
}

// History is four checkpoints and a week holds many more, so a ruling from
// three days ago, eight checkpoints back, was inside the window and still
// never shown: revival skipped it for being in the window, and History never
// reached it. Not rendered is the gate, not outside the window — and a ruling
// from inside it is not labelled as from outside it.
func TestARulingPastHistorysReachButInsideTheWindowComesBack(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	seedBehindHistory(t, ix, now, gitstate.State{}, oldCheckpoint{age: 3 * 24 * time.Hour, failed: []string{oldWaveguide}})
	p, out := resumeKestrel(t, ix, nil, "", now, SinceWeek, "")
	if len(p.Revived) != 1 || p.Revived[0].Text != oldWaveguide {
		t.Fatalf("revived = %+v, want the waveguide ruling from inside the window\n%s", p.Revived, out)
	}
	if !strings.Contains(out, "- (cursor, 3d ago) re-quoting the waveguide") {
		t.Errorf("the in-window ruling is not labelled with its author and age alone:\n%s", out)
	}
}

// since: all turns the window off, which turned revival off with it, so the
// one request that asks for everything got less than an inferred week.
func TestUnderSinceAllARelevantRulingPastHistorysReachComesBack(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	seedBehindHistory(t, ix, now, gitstate.State{}, oldCheckpoint{age: 50 * 24 * time.Hour, failed: []string{oldWaveguide, oldTailwind}})
	p, out := resumeKestrel(t, ix, nil, "", now, SinceAll, "")
	if len(p.Revived) != 1 || p.Revived[0].Text != oldWaveguide {
		t.Fatalf("revived = %+v, want the waveguide ruling under since: all\n%s", p.Revived, out)
	}
	if strings.Contains(out, "outside the window") {
		t.Errorf("a ruling is said to be outside a window that was never applied:\n%s", out)
	}
	if !strings.Contains(out, "_1 ruled-out approach from older checkpoints is listed because it matches this task by keyword only (no embedding model)._") {
		t.Errorf("with no window line, nothing says how the older rulings were weighed:\n%s", out)
	}
}

// "Everything recorded falls inside it. 1 older ruled-out approach from before
// it was checked" said both that nothing was outside the window and that
// something from before it was weighed. With older rulings weighed, the line
// says only what is true: nothing in the window was set aside.
func TestTheWindowLineDoesNotSayEverythingFallsInsideItBesideOlderRulingsItWeighed(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	seedBehindHistory(t, ix, now, gitstate.State{}, oldCheckpoint{age: 50 * 24 * time.Hour, failed: []string{oldTailwind}})
	p, out := resumeKestrel(t, ix, nil, "", now, SinceWeek, "")
	if p.OutOfWindow != 0 || len(p.Revived) != 0 {
		t.Fatalf("OutOfWindow = %d, revived = %+v; want neither\n%s", p.OutOfWindow, p.Revived, out)
	}
	if strings.Contains(out, "Everything recorded falls inside it") {
		t.Errorf("the window line says everything is inside it while counting rulings from before it:\n%s", out)
	}
	if !strings.Contains(out, "Nothing in it was set aside. 1 ruled-out approach in older checkpoints was checked against this task by keyword only (no embedding model); none matched.") {
		t.Errorf("the window line does not account for the older ruling it weighed:\n%s", out)
	}
}

// A dead end recorded in two old checkpoints is one approach weighed against
// the task. Counted per occurrence, "2 checked" overstated the comparison.
func TestAnOlderRulingRecordedInTwoCheckpointsIsCountedOnceAsWeighed(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	seedBehindHistory(t, ix, now, gitstate.State{},
		oldCheckpoint{age: 50 * 24 * time.Hour, failed: []string{oldTailwind}},
		oldCheckpoint{age: 40 * 24 * time.Hour, failed: []string{oldTailwind}})
	p, out := resumeKestrel(t, ix, nil, "", now, SinceWeek, "")
	if p.ReviveConsidered != 1 {
		t.Errorf("ReviveConsidered = %d, want 1 for one ruling in two checkpoints\n%s", p.ReviveConsidered, out)
	}
	if !strings.Contains(out, "1 ruled-out approach in older checkpoints was checked") {
		t.Errorf("the window line does not count the ruling once:\n%s", out)
	}
}

// Revival ran after the drift measurement, so the oldest rulings in the pack —
// the likeliest to have been overtaken by the code — were the only ones that
// never said their file had changed.
func TestARevivedRulingWhoseFileChangedSinceCarriesTheDriftWarning(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, "quote/waveguide.go", "fixed price\n")
	_, anchor := gitstate.Head(repo)
	remote, root := gitstate.Identity(repo)
	git := gitstate.State{Branch: "main", Commit: anchor, Remote: remote, Root: root}
	ix := seedVault(t)
	now := time.Now()
	ruling := "re-quoting the waveguide supplier for a lower price — quote/waveguide.go caps it"
	seedBehindHistory(t, ix, now, git, oldCheckpoint{age: 50 * 24 * time.Hour, failed: []string{ruling}, git: git})
	commitFile(t, repo, "quote/waveguide.go", "negotiated price\n")

	p, out := resumeKestrel(t, ix, nil, "", now, SinceWeek, repo)
	if len(p.Revived) != 1 {
		t.Fatalf("revived = %+v, want the waveguide ruling\n%s", p.Revived, out)
	}
	if !strings.Contains(out, "outside the window) "+ruling+" — ⚠ recorded at "+anchor+"; quote/waveguide.go changed in 1 commit since") {
		t.Errorf("the revived ruling about a changed file is handed over as settled:\n%s", out)
	}
}

// contentEmbedder gives the query and the filler notes one direction and the
// old checkpoint another nearby, so with seventy fillers the checkpoint sits
// around rank seventy in the vector arm, as it does in the keyword arm.
func contentEmbedder(t *testing.T) *provider.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		data := make([]map[string]any, len(req.Input))
		for i, in := range req.Input {
			v := []float32{0, 0, 1}
			switch {
			case strings.Contains(in, "FILLER"), strings.HasPrefix(in, "resume work on"):
				v = []float32{1, 0, 0}
			case strings.Contains(in, "re-quoting"):
				v = []float32{0.6, 0.8, 0}
			}
			data[i] = map[string]any{"embedding": v}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return provider.New("fake", srv.URL, "")
}

// The fused-score gate assumed every rank sat under RRF's 60, so a checkpoint
// both arms found at rank seventy scored under one arm's best and was dropped
// as a one-arm guess. Membership is checked in each arm directly.
func TestACheckpointBothArmsFindPastRankSixtyStillCountsAsBothArms(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	for i := 0; i < 70; i++ {
		path := filepath.Join(ix.Vault, "filler", fmt.Sprintf("f%02d.md", i))
		os.MkdirAll(filepath.Dir(path), 0o755)
		words := "kestrel one lower waveguide cost second supplier quote"
		body := fmt.Sprintf("# FILLER %d %s\n\n%s\n", i, words, strings.Repeat(words+" ", 4))
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// FTS weighs a word by how few notes hold it; with only fillers in the
	// vault every query word is in nearly every note and weighs nothing, so
	// unrelated notes give the query's words the rarity a real vault does.
	for i := 0; i < 300; i++ {
		path := filepath.Join(ix.Vault, "background", fmt.Sprintf("b%03d.md", i))
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(fmt.Sprintf("# Background %d\n\nan unrelated note about gardening, number %d\n", i, i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seedBehindHistory(t, ix, now, gitstate.State{}, oldCheckpoint{age: 50 * 24 * time.Hour, failed: []string{oldWaveguide}})
	embed := contentEmbedder(t)
	if _, err := ix.EmbedPending(embed, "m", 500); err != nil {
		t.Fatal(err)
	}
	p, out := resumeKestrel(t, ix, embed, "m", now, SinceWeek, "")
	if p.ReviveLexical || p.ReviveEmbedErr != nil {
		t.Fatalf("the embedding arm was not used: lexical=%v err=%v", p.ReviveLexical, p.ReviveEmbedErr)
	}
	if len(p.Revived) != 1 || p.Revived[0].Text != oldWaveguide {
		t.Errorf("revived = %+v, want the waveguide ruling both arms found\n%s", p.Revived, out)
	}
}

// A model runtime that is configured and down is not "no embedding model":
// the pack says the model failed, and weighs by keyword rather than not at all.
func TestAnEmbeddingModelThatFailsIsReportedAndRelevanceFallsBackToKeywords(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	seedBehindHistory(t, ix, now, gitstate.State{}, oldCheckpoint{age: 50 * 24 * time.Hour, failed: []string{oldWaveguide}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not loaded", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	p, out := resumeKestrel(t, ix, provider.New("fake", srv.URL, ""), "m", now, SinceWeek, "")
	if p.ReviveEmbedErr == nil || len(p.Revived) != 1 {
		t.Fatalf("embed err = %v, revived = %+v; want the failure kept and the keyword match made\n%s", p.ReviveEmbedErr, p.Revived, out)
	}
	if !strings.Contains(out, "by keyword only (the embedding model failed: ") {
		t.Errorf("the pack does not say the embedding model failed:\n%s", out)
	}
}

// Build filters the checkpoint it hands over to this repository, and the cost
// count read the project's history again without that filter, so another
// repository under the same name counted as a session that hit this one's
// dead end.
func TestAnEarlierSessionInAnotherRepositoryIsNotCountedAsHittingTheDeadEnd(t *testing.T) {
	work, personal := gitRepo(t), gitRepo(t)
	ix := seedVault(t)
	now := time.Now()
	ruling := "caching the cart — the cache evicts on every write, so it never hits"
	for _, c := range []struct {
		dir, host string
		age       time.Duration
	}{{personal, "sess-personal", 48 * time.Hour}, {work, "sess-work", time.Hour}} {
		remote, root := gitstate.Identity(c.dir)
		if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
			Project: "shop", Agent: "claude", HostSession: c.host, Task: "speed up checkout",
			Failed: []string{ruling}, TS: now.Add(-c.age).Unix(),
			Git: gitstate.State{Branch: "main", Commit: "abc1234", Remote: remote, Root: root},
		}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "shop", Dir: work, Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if p.Checkpoint == nil || p.Checkpoint.HostSession != "sess-work" {
		t.Fatalf("checkpoint = %+v, want this repository's", p.Checkpoint)
	}
	if p.Cost != nil {
		t.Errorf("another repository's session was counted as hitting this dead end: %+v\n%s", *p.Cost, p.Render())
	}
}
