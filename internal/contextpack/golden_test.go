package contextpack

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/session"
)

// What the resuming agent reads is the product, and every assertion elsewhere
// in this package checks one phrase of it. An excerpt that lost its first
// sentence (1c) passed all of them: nothing looked at the page as a whole. These
// snapshots do, so a change to what a resume says shows up as a diff a reviewer
// reads, not as drift nobody notices.
//
//	go test ./internal/contextpack -run Golden -update
var update = flag.Bool("update", false, "rewrite the golden resume snapshots in testdata/golden")

// goldenDir is resolved while the working directory is still this package: each
// fixture moves into a temporary one, and a relative path read from there
// finds nothing and writes the snapshots somewhere they are lost.
var goldenDir, goldenDirErr = filepath.Abs(filepath.Join("testdata", "golden"))

// The fixtures are written in Go rather than kept as vault files because a
// checkpoint's age is part of what renders ("Last checkpoint by codex, 3h
// ago"), and a fixed timestamp in a file would read a day older every day. Every
// time is set relative to now, and scrub takes out the absolute dates and file
// stamps that follow from it. Every checkpoint carries its repository state
// too: Commit reads an empty one from the working directory, and a TMPDIR
// inside a checkout put that checkout's branch into the snapshot.
var goldenFixtures = []struct {
	name   string
	budget int
	seed   func(t *testing.T) (*index.Index, string)
}{
	{"empty-vault", 0, func(t *testing.T) (*index.Index, string) { return emptyVault(t), "kestrel-one" }},
	{"handoff-chain", 0, handoffChain},
	{"handoff-chain-tight-budget", 120, handoffChain},
	{"history-without-a-note", 0, historyWithoutANote},
}

func TestResumePacksMatchTheirGoldenSnapshots(t *testing.T) {
	if goldenDirErr != nil {
		t.Fatalf("could not resolve the snapshot directory: %v", goldenDirErr)
	}
	for _, f := range goldenFixtures {
		t.Run(f.name, func(t *testing.T) {
			ix, project := f.seed(t)
			// The request `logos resume <project>` makes, less the directory
			// check: a fixture vault has no repository to compare against.
			p, err := Build(ix, nil, "", Request{
				Task: "resume work on " + project, Hint: project, Agent: "claude-code", Budget: f.budget,
			})
			if err != nil {
				t.Fatal(err)
			}
			got := scrub(p.Render())

			path := filepath.Join(goldenDir, f.name+".md")
			if *update {
				if err := os.MkdirAll(goldenDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v — run with -update to write it", err)
			}
			if got != string(want) {
				t.Errorf("the resume pack for %s no longer matches %s at %s; if the change is intended, rerun with -update and review the diff.\n--- got ---\n%s",
					f.name, path, firstDifference(got, string(want)), got)
			}
		})
	}
}

// -update writes the snapshots the fixtures make and nothing else, so one left
// by a renamed or deleted fixture would sit there uncompared, still read by
// reviewers as describing something.
func TestEveryGoldenSnapshotHasAFixtureThatWritesIt(t *testing.T) {
	entries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatal(err)
	}
	written := map[string]bool{}
	for _, f := range goldenFixtures {
		written[f.name+".md"] = true
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") && !written[e.Name()] {
			t.Errorf("no fixture writes %s; delete it or restore its fixture", e.Name())
		}
	}
}

// firstDifference names the line a long page first parts from its snapshot,
// which is where a reader of the failure needs to look.
func firstDifference(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("line %d: got %q, want %q", i+1, gl, wl)
		}
	}
	return "no line"
}

var (
	absoluteDate = regexp.MustCompile(`\b\d{1,2} (Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) \d{4}\b`)
	fileStamp    = regexp.MustCompile(`\b\d{8}-\d{6}\b`)
	// The token figures in the footer, but not the limit asked for: "1 Nov
	// 2026" is a character shorter than "31 Oct 2026", so the estimates move
	// with the day the test runs, while the text they estimate is snapshotted
	// above them anyway.
	tokenEstimate = regexp.MustCompile(`(?m)^(_(?:Context budget|Actual size|The budget left out).*)$`)
	approxFigure  = regexp.MustCompile(`~\d+`)
	sectionFigure = regexp.MustCompile(`([a-z]) \d+([,)])`)
)

// scrub replaces what changes with the day and timezone the test runs in and
// nothing else, so a snapshot differs only when the render does.
func scrub(s string) string {
	s = absoluteDate.ReplaceAllString(s, "<date>")
	s = fileStamp.ReplaceAllString(s, "<stamp>")
	return tokenEstimate.ReplaceAllStringFunc(s, func(line string) string {
		return sectionFigure.ReplaceAllString(approxFigure.ReplaceAllString(line, "~N"), "$1 N$2")
	})
}

// emptyVault is the first resume after setup: nothing written yet.
func emptyVault(t *testing.T) *index.Index {
	t.Helper()
	return openVault(t, t.TempDir())
}

// commitAt writes a fixture checkpoint under the name it would have had at
// its age. Commit names the file by the second it runs, and checkpoints are
// ordered by name, so three written in one second came out in agent-name
// order and the latest was whichever agent sorted last.
func commitAt(t *testing.T, ix *index.Index, c *session.Checkpoint) {
	t.Helper()
	if err := session.Commit(ix.DB, ix.Vault, c); err != nil {
		t.Fatal(err)
	}
	from := filepath.Join(ix.Vault, c.Slug+".md")
	to := filepath.Join(filepath.Dir(from), time.Unix(c.TS, 0).Format("20060102-150405")+"-"+c.Agent+".md")
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}

// handoffChain is the case resume exists for: three agents over three days,
// the first one's dead end still dead, the last one stopped mid-task with
// uncommitted work, progress noted since and a standing preference on record.
func handoffChain(t *testing.T) (*index.Index, string) {
	t.Helper()
	ix := seedVault(t)
	now := time.Now()
	for _, c := range []*session.Checkpoint{
		{Agent: "aider", Task: "cut the BOM", TS: now.Add(-74 * time.Hour).Unix(),
			Git:       gitstate.State{Branch: "main", Commit: "4c1a9e2", Subject: "Price the BOM at the 30k quote"},
			Intent:    "the first production run is priced at the $118 target or it does not happen",
			Failed:    []string{"re-quoting the waveguide — no movement under 10k units"},
			Decisions: []string{"Keep the 71 percent bonding line, because the second source needs a new jig"},
			Next:      "quote the single-mic line"},
		{Agent: "codex", Task: "cut the BOM", TS: now.Add(-27 * time.Hour).Unix(),
			Git:      gitstate.State{Branch: "bom-cut", Commit: "8d03f7b", Subject: "Quote the single-mic line"},
			Verified: []string{"single-mic quote came back at $4.10 a unit (supplier email, 30k)"},
			Failed:   []string{"dropping the second mic — beamforming fails the noisy-room test"},
			Next:     "compare the battery quotes"},
		{Agent: "cursor", Task: "cut the BOM", TS: now.Add(-3 * time.Hour).Unix(),
			Git: gitstate.State{Branch: "bom-cut", Commit: "b71e5c0", Subject: "Compare the battery quotes",
				Dirty: 2, Files: []string{"topics/bom-cost.md", "quotes/battery-b.md"}, Insertions: 14, Deletions: 3},
			Verified:  []string{"battery quote B is $2.30 cheaper at the same capacity"},
			Blockers:  []string{"quote B's cell has no UL certificate on file"},
			Questions: []string{"does the enclosure still close with quote B's 0.4mm thicker cell?"},
			Files:     []string{"topics/bom-cost.md"},
			Next:      "ask the supplier for quote B's UL certificate"},
	} {
		c.Project = "kestrel-one"
		commitAt(t, ix, c)
	}
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AddNoteAt(ix.DB, "kestrel-one", "cursor", "emailed the supplier for the UL certificate", now.Add(-2*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Store(ix.DB, nil, "", &memory.Memory{
		Text: "Prices are quoted per unit at 30k volume unless stated otherwise", Kind: memory.Preference,
		Project: "kestrel-one", Salience: 0.8, Confidence: 0.9, Source: "manual",
	}); err != nil {
		t.Fatal(err)
	}
	return ix, "kestrel-one"
}

// historyWithoutANote is a project an agent only ever reached through MCP:
// checkpoints and a fact, and no project note for resolve to find.
func historyWithoutANote(t *testing.T) (*index.Index, string) {
	t.Helper()
	ix := emptyVault(t)
	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "shop", Agent: "claude", Task: "move payments to stripe v3",
		TS:       time.Now().Add(-5 * time.Hour).Unix(),
		Git:      gitstate.State{Branch: "stripe-v3", Commit: "e09a113", Subject: "Move charges to the v3 client"},
		Verified: []string{"go test ./payments passes against the v3 test keys"},
		Failed:   []string{"upgrading the SDK in place — v3 drops the charges API the refunds path calls"},
		Next:     "port refunds to payment intents",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Store(ix.DB, nil, "", &memory.Memory{
		Text: "The staging database is reset every Sunday at 03:00 UTC", Kind: memory.Fact,
		Project: "shop", Source: "manual",
	}); err != nil {
		t.Fatal(err)
	}
	return ix, "shop"
}
