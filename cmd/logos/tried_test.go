package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/deadend"
)

// The gap a beta tester found: `logos tried` could only ask, never answer.
//
// A dead end could be recorded exactly one way — `checkpoint --failed` — so an
// approach ruled out an hour into a session was invisible to every other agent
// until that session ended, and lost entirely when it ended without a
// checkpoint. This is the recording half, and the property that matters is not
// that a note was written but that the *query* half finds it.
func TestTriedRecordsARuledOutApproachWhereTheNextCheckWillFindIt(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)

	if err := runTried([]string{
		"cache the embeddings in the index",
		"--ruled-out", "a reindex drops them and recall goes flat with no warning",
		"--project", "kestrel", "--layer", "design", "--scope", "general",
	}); err != nil {
		t.Fatalf("recording a dead end returned an error: %v", err)
	}

	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	hits, err := deadend.Check(ix.Vault, ix.DB, nil, "", "cache the embeddings in the index", "kestrel", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("the approach was just ruled out and `tried` should now find it")
	}
	if hits[0].Record.Observation == "" {
		t.Errorf("the reason it was ruled out is the whole point and did not survive: %+v", hits[0].Record)
	}
	if hits[0].Record.Layer != deadend.LayerDesign || hits[0].Record.Scope != deadend.ScopeGeneral {
		t.Errorf("the typing given on the command line did not survive: %+v", hits[0].Record)
	}
}

// A misspelled enum would otherwise be dropped by ParseRecord, recording a
// ruling that quietly lost the field, so the refusal is the feature.
func TestTriedRefusesATypedFieldItWouldSilentlyDrop(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)

	err := runTried([]string{
		"switch the queue to Redis",
		"--ruled-out", "it loses jobs on restart",
		"--project", "kestrel", "--layer", "architecture",
	})
	if err == nil {
		t.Fatal("an unrecognised --layer should be refused, not recorded with the field missing")
	}
}

// --ruled-out with nothing after it records an approach with no reason, which
// is the one thing a ruling may not be: the next agent gets a veto it cannot
// evaluate.
func TestTriedRefusesToRuleSomethingOutWithoutSayingWhy(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)

	if err := runTried([]string{"switch the queue to Redis", "--ruled-out", "--project", "kestrel"}); err == nil {
		t.Fatal("a ruling with no observation should be refused")
	}
}

// `logos tried` chose its embedder from the router alone. LOGOS_EMBED=off
// still sent the proposal and every ruling to a runtime that was up, and a
// model LOGOS_EMBED named was ignored for the router's default — query
// vectors of another model than the one `logos index` and `mcp serve` use.
func TestTriedEmbedsWithTheModelLOGOS_EMBEDNamesAndNotAtAllWhenOff(t *testing.T) {
	rt := newRecordingRuntime(t, "custom-embed", "nomic-embed-text")
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_RUNTIME", rt.URL)
	t.Setenv("LOGOS_EMBED", "off")

	if err := runTried([]string{"cache the embeddings in the index", "--ruled-out", "a reindex drops them",
		"--project", "kestrel"}); err != nil {
		t.Fatal(err)
	}
	if err := runTried([]string{"cache the embeddings in the index", "--project", "kestrel"}); err != nil {
		t.Fatal(err)
	}
	if got := rt.embedded(); len(got) != 0 {
		t.Fatalf("LOGOS_EMBED=off still embedded with %v", got)
	}

	t.Setenv("LOGOS_EMBED", "custom-embed")
	if err := runTried([]string{"cache the embeddings in the index", "--project", "kestrel"}); err != nil {
		t.Fatal(err)
	}
	for _, m := range rt.embedded() {
		if m != "custom-embed" {
			t.Errorf("tried embedded with %q, not the custom-embed LOGOS_EMBED names", m)
		}
	}
	if len(rt.embedded()) == 0 {
		t.Error("tried did not embed at all with a model named")
	}
}

// tried embedded through the 300 s generation client, so a runtime that listed
// its models and then never answered held it — and the hook shelling out to
// it — for minutes (#244). It now gives up at its own timeout and says the
// semantic check was skipped.
func TestTriedStopsWaitingOnARuntimeThatNeverAnswers(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"data":[{"id":"nomic-embed-text"}]}`)
			return
		}
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	old := triedEmbedTimeout
	triedEmbedTimeout = 200 * time.Millisecond
	t.Cleanup(func() { triedEmbedTimeout = old })
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_RUNTIME", srv.URL)
	t.Setenv("LOGOS_EMBED", "")
	if err := runTried([]string{"cache the embeddings in the index", "--ruled-out", "a reindex drops them",
		"--project", "kestrel"}); err != nil {
		t.Fatal(err)
	}

	done := make(chan string, 1)
	go func() {
		done <- captureStdout(t, func() {
			if err := runTried([]string{"keep the vectors in sqlite", "--project", "kestrel"}); err != nil {
				t.Error(err)
			}
		})
	}()
	select {
	case out := <-done:
		if !strings.Contains(out, "semantic check skipped") {
			t.Errorf("tried gave up on the runtime without saying so:\n%s", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("tried was still waiting on a runtime that never answers after 10 s")
	}
}
