package deadend_test

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/session"
)

func TestARuledOutLineNamesTheTranscriptTurnsItCameFrom(t *testing.T) {
	dir := t.TempDir()
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*session.Checkpoint{
		{Project: "kestrel", Agent: "claude-code", HostSession: "abcdef12-0000-4000-8000-000000000001", Turns: 40,
			Task: "spike", Next: "go on"},
		{Project: "kestrel", Agent: "claude-code", HostSession: "abcdef12-0000-4000-8000-000000000001", Turns: 63,
			Task: "spike", Next: "go on", Failed: []string{"Switching to a plastic frame, fails the drop test at 1.2m"}},
	} {
		if err := session.Commit(ix.DB, dir, c); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := deadend.Collect(dir, ix.DB, "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	out := deadend.Render("switch to a plastic frame", hits)
	if !strings.Contains(out, "source: turns 41-63 of session abcdef12, `logos transcript ") {
		t.Fatalf("the ruled-out line does not name its turns:\n%s", out)
	}

	// Rendered at read time only: the checkpoint body must not carry it, or the
	// collector would read the pointer back as a ruling.
	cps, _ := session.History(dir, "kestrel", 0)
	for _, c := range cps {
		for _, f := range c.Failed {
			if strings.Contains(f, "source:") {
				t.Fatalf("a pointer was written into the checkpoint: %q", f)
			}
		}
	}
}

func TestARuledOutLineFromACheckpointWithNoTurnsNamesNoSource(t *testing.T) {
	dir := t.TempDir()
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, dir, &session.Checkpoint{Project: "kestrel", Agent: "claude-code",
		HostSession: "abcdef12-0000-4000-8000-000000000001", Task: "spike", Next: "go on",
		Failed: []string{"Switching to a plastic frame, fails the drop test at 1.2m"}}); err != nil {
		t.Fatal(err)
	}
	hits, _ := deadend.Collect(dir, ix.DB, "kestrel")
	if out := deadend.Render("switch to a plastic frame", hits); strings.Contains(out, "source:") {
		t.Fatalf("a source was named though no turn count is known:\n%s", out)
	}
}
