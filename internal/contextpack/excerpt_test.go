package contextpack

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/index"
)

// turns builds a transcript-shaped body: n filler turns with one distinctive
// turn in the middle.
func turns(n int, middle string) string {
	var ps []string
	for i := 0; i < n; i++ {
		if i == n/2 {
			ps = append(ps, middle)
			continue
		}
		ps = append(ps, fmt.Sprintf("**assistant:** %s", strings.Repeat("general chatter about nothing in particular ", 12)))
	}
	return strings.Join(ps, "\n\n")
}

func notePack(budget int, task string, hits ...index.Hit) *Pack {
	p := &Pack{Task: task, Notes: hits}
	p.Budget.Limit = budget
	return p
}

func TestALongNoteIsExcerptedSoTheNextRankedNoteStillFits(t *testing.T) {
	long := index.Hit{Slug: "topics/long", Title: "Long", Body: turns(60, "**user:** I adopted a greyhound named Biscuit in March.")}
	next := index.Hit{Slug: "topics/next", Title: "Next", Body: "**user:** My sister lives in Lisbon and teaches violin."}
	p := notePack(2000, "where does my sister live", long, next)
	out := p.Render()
	if !strings.Contains(out, "topics/next") || !strings.Contains(out, "Lisbon") {
		t.Fatalf("the second note was crowded out by the first:\n%s", out)
	}
	if !strings.Contains(out, "excerpt:") || !strings.Contains(out, "the full note is at `topics/long`") {
		t.Errorf("the cut note does not say it is an excerpt:\n%s", out)
	}
	if p.Budget.Spent > p.Budget.Limit {
		t.Errorf("spent %d of %d", p.Budget.Spent, p.Budget.Limit)
	}
	for _, l := range p.Budget.By {
		if l.Section == secNotes && (l.Items != 2 || l.Dropped != 0) {
			t.Errorf("notes line = %+v, want 2 kept and none dropped", l)
		}
	}
	if p.Budget.Candidates < estimate(long.Body) {
		t.Errorf("candidates %d forgot the uncut cost of the excerpted note", p.Budget.Candidates)
	}
}

func TestAShortNoteIsStillPackedWhole(t *testing.T) {
	short := index.Hit{Slug: "topics/short", Title: "Short", Body: "A short note.\n\nWith two paragraphs."}
	long := index.Hit{Slug: "topics/long", Title: "Long", Body: turns(60, "needle")}
	out := notePack(2000, "needle", long, short).Render()
	if !strings.Contains(out, "With two paragraphs.") || strings.Contains(out, "`topics/short`_") {
		t.Errorf("a short note was cut:\n%s", out)
	}
}

func TestNothingIsExcerptedWhileEveryNoteFits(t *testing.T) {
	a := index.Hit{Slug: "topics/a", Title: "A", Body: turns(10, "needle")}
	out := notePack(16000, "needle", a).Render()
	if strings.Contains(out, "excerpt:") {
		t.Errorf("a note that fit was excerpted:\n%s", out)
	}
}

func TestAnExcerptKeepsThePassageThatMatchesTheTask(t *testing.T) {
	body := turns(60, "**user:** I adopted a greyhound named Biscuit in March.")
	got, cut := excerptBody(body, map[string]bool{"greyhound": true, "adopted": true}, 300)
	if !cut || !strings.Contains(got, "Biscuit") {
		t.Fatalf("cut=%v, the matching passage is missing:\n%s", cut, got)
	}
	if estimate(got) > 300 {
		t.Errorf("excerpt is %d tokens, over its 300 cap", estimate(got))
	}
}

func TestAnExcerptKeepsPassagesInDocumentOrder(t *testing.T) {
	body := "alpha passage zebra\n\nfiller filler filler filler filler filler filler filler\n\nbeta passage zebra"
	got, _ := excerptBody(body, map[string]bool{"zebra": true}, 20)
	if a, b := strings.Index(got, "alpha"), strings.Index(got, "beta"); a < 0 || b < a {
		t.Errorf("passages out of order or missing:\n%s", got)
	}
}
