package main

import (
	"testing"

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
