// Package deadend answers one question: has this been tried before?
//
// Every other retrieval path here is pull — an agent asks, and the store
// answers. This one is push. The agent is about to propose something, and the
// useful move is to interrupt: *you tried that in March, here is why it did not
// work, here is who found out.*
//
// The distinction matters more than it looks. `resume` gives an arriving agent
// the last checkpoint, which covers the approach ruled out yesterday on the
// project it is already working on. It does nothing for the approach ruled out
// in March, on a project nobody mentioned, by an agent that no longer exists.
// That knowledge is in the vault — it just has no way of reaching the moment it
// would have been useful, because nobody thinks to search for the thing they
// are about to suggest.
//
// So the corpus is every dead end recorded anywhere in the vault, and the query
// is the proposal itself. This is also the part of the system that cannot be
// copied: the mechanism is a morning's work, and the two years of accumulated
// "we tried that" is not.
//
// # What counts as a dead end
//
// Checkpoints carry a Failed list, which is the curated corpus — someone chose
// to write those down as ruled out. Working notes are messier and matter
// anyway: an agent killed before it could check in leaves its findings only
// there, and "the audio team vetoed dropping the second mic" is exactly the
// kind of thing that dies with the session. Those are admitted when they read
// as a failure, by an explicit marker rather than by sentiment, because a note
// wrongly filed as a dead end will stop work that should have happened.
package deadend

import (
	"database/sql"
	"math"
	"sort"
	"strings"

	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/session"
	"github.com/pragun/brain/internal/textmatch"
)

// Source says where a ruling came from, which is also how much to trust it.
type Source string

const (
	// FromCheckpoint is a Failed entry someone deliberately recorded.
	FromCheckpoint Source = "checkpoint"
	// FromNote is a working note that reads as a failure. Never committed, so
	// it may be a first impression rather than a conclusion.
	FromNote Source = "working note"
)

// A Ruling is something already tried that did not work.
type Ruling struct {
	Text    string  `json:"text"`
	Project string  `json:"project"`
	Agent   string  `json:"agent"`
	When    int64   `json:"when"`
	Slug    string  `json:"slug,omitempty"`
	Source  Source  `json:"source"`
	Score   float64 `json:"score"`
	// Elsewhere is true when this was ruled out on a different project than the
	// one being asked about. Still worth saying, and worth flagging: what failed
	// on other hardware may not fail here, and presenting it as settled would be
	// the same overreach this package exists to prevent.
	Elsewhere bool `json:"elsewhere"`
}

// failureMarkers are how people write down that something did not work.
//
// Explicit markers rather than a sentiment judgement. The cost of a miss is
// that an agent rediscovers a dead end; the cost of a false positive is that it
// abandons a live approach because a note sounded negative. Those are not
// symmetric, so this list only matches people saying it outright.
var failureMarkers = []string{
	"didn't work", "did not work", "doesn't work", "does not work",
	"won't work", "will not work", "failed", "fails ", "fails.", "failing",
	"vetoed", "rejected", "ruled out", "dead end", "no movement",
	"not viable", "unworkable", "gave up", "abandoned", "backed out",
	"blocked by", "no good", "not possible", "cannot", "can't ",
}

func readsAsFailure(s string) bool {
	low := strings.ToLower(s)
	for _, m := range failureMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// depth is how many checkpoints back to read per project. Dead ends do not
// expire, so this is generous — the whole point is reaching work old enough
// that nobody remembers it.
const depth = 50

// Collect gathers every recorded dead end. An empty project means the whole
// vault, which is the interesting case: the approach that failed on one project
// is often the same approach about to be proposed on another.
func Collect(vaultDir string, db *sql.DB, project string) ([]Ruling, error) {
	projects := []string{project}
	if strings.TrimSpace(project) == "" {
		var err error
		if projects, err = session.Projects(vaultDir); err != nil {
			return nil, err
		}
	}

	var out []Ruling
	for _, proj := range projects {
		history, err := session.History(vaultDir, proj, depth)
		if err != nil {
			continue // one unreadable project must not hide the others
		}
		for _, c := range history {
			for _, f := range c.Failed {
				if strings.TrimSpace(f) == "" {
					continue
				}
				out = append(out, Ruling{
					Text: textmatch.Flatten(f), Project: proj, Agent: c.Agent,
					When: c.TS, Slug: c.Slug, Source: FromCheckpoint,
				})
			}
		}

		if db == nil {
			continue
		}
		notes, err := session.Uncommitted(db, proj)
		if err != nil {
			continue
		}
		for _, n := range notes {
			if readsAsFailure(n.Text) {
				out = append(out, Ruling{
					Text: textmatch.Flatten(n.Text), Project: proj, Agent: n.Agent,
					When: n.TS, Source: FromNote,
				})
			}
		}
	}
	return out, nil
}

// cosineFloor is when two phrasings of an approach count as the same approach.
//
// Higher than a retrieval threshold would be. Retrieval that surfaces a
// marginal note costs a glance; an interruption that fires on a marginal match
// costs credibility, and a checker an agent learns to ignore is worse than no
// checker — it is the same knowledge, now with a reason not to look.
const cosineFloor = 0.74

// Check returns the recorded dead ends bearing on a proposed approach, best
// match first.
//
// Two ways to match, either sufficient. Lexical containment catches a proposal
// restating the original almost word for word, which is the common case when an
// agent has read the project note and is working from the same vocabulary.
// Embedding similarity catches the opposite case — the same idea in different
// words — and is what makes this useful across projects, where the vocabulary
// has no reason to line up. With no embedder available the lexical arm still
// works, so this degrades rather than disappearing.
func Check(vaultDir string, db *sql.DB, p *provider.Provider, embedModel, proposed, project string, k int) ([]Ruling, error) {
	if strings.TrimSpace(proposed) == "" {
		return nil, nil
	}
	corpus, err := Collect(vaultDir, db, "")
	if err != nil {
		return nil, err
	}
	if len(corpus) == 0 {
		return nil, nil
	}

	asked := textmatch.Subject(proposed)
	lexical := make([]float64, len(corpus))
	for i, r := range corpus {
		lexical[i] = textmatch.Overlap(asked, textmatch.Subject(r.Text))
	}

	semantic := make([]float64, len(corpus))
	if p != nil {
		texts := make([]string, 0, len(corpus)+1)
		texts = append(texts, proposed)
		for _, r := range corpus {
			texts = append(texts, r.Text)
		}
		if vecs, err := p.Embed(embedModel, texts); err == nil && len(vecs) == len(texts) {
			for i := range corpus {
				semantic[i] = cosine(vecs[0], vecs[i+1])
			}
		}
	}

	var hits []Ruling
	for i, r := range corpus {
		if lexical[i] < textmatch.Related && semantic[i] < cosineFloor {
			continue
		}
		r.Score = math.Max(lexical[i], semantic[i])
		r.Elsewhere = project != "" && r.Project != project
		hits = append(hits, r)
	}

	sort.SliceStable(hits, func(a, b int) bool {
		// Same-project rulings lead: they are about this hardware, this
		// codebase, this constraint, and need no caveat to act on.
		if hits[a].Elsewhere != hits[b].Elsewhere {
			return !hits[a].Elsewhere
		}
		return hits[a].Score > hits[b].Score
	})
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
