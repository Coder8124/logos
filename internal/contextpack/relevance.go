package contextpack

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/textmatch"
	"github.com/Coder8124/logos/internal/vault"
)

// Revived is a ruled-out approach from a checkpoint older than the handful
// History reaches, handed over because it bears on the task. It carries its
// own date so the render can say how old it is: a month-old ruling read as
// this week's is a dead end treated as fresh news.
type Revived struct {
	Text  string `json:"text"`
	Agent string `json:"agent,omitempty"`
	TS    int64  `json:"ts"`
	Slug  string `json:"slug,omitempty"`
	// Commit is the checkpoint's own, the anchor its drift is measured from;
	// left out of the JSON so that path stays what it was.
	Commit string `json:"-"`
}

// maxRevived bounds what relevance may bring back past History. Three is a
// line each inside the checkpoint's fixed tier: enough for the one dead end
// that matters to arrive, too few for a vocabulary-heavy project to turn the
// pack back into "everything ever ruled out".
const maxRevived = 3

// reviveReach is how deep each search arm is read: the pool HybridSearch
// fuses for its usual 30. Checkpoints compete with every note in the vault for
// these ranks, so it is wider than maxNotes, and still one query per arm.
const reviveReach = 120

// reviveRelevant brings back older rulings History is too short to reach and
// the task reaches. History is the last few checkpoints, a window is a guess
// about which period matters, and a dead end expires with neither: the one
// ruled out on exactly this subject eight checkpoints ago is the one an agent
// is most likely to walk back into, whether that was yesterday or two months
// ago. So the gate is "not already rendered", never "outside the window".
//
// The checkpoints are found through the same index arms search and recall
// use, so relevance means what it means everywhere else, and they are read
// back from earlier — the vault's own files — so a deleted index loses nothing
// here either. Two gates, both already in use: with an embedding model the
// checkpoint must be in both arms, so the embedding and the keywords agreed it
// is about this task rather than one of them guessing; and each ruling must
// match the task the way before_you_try matches a proposal by its words
// (textmatch.Related containment, two shared words). A ruling that only shares
// the project's name is not about the task.
func (p *Pack) reviveRelevant(ix *index.Index, embed *provider.Provider, embedModel, query string, req Request, reviews map[string]session.Review, earlier []session.Checkpoint) {
	c := p.Checkpoint
	if c == nil || p.historyScope == "" || ix.Vault == "" {
		return
	}
	// The checkpoint's task and next step join the query for the reason they
	// join recall's: resume's own task is "resume work on X", which names no
	// subject at all.
	q := strings.TrimSpace(query + " " + c.Task + " " + c.Next)
	lex, err := ix.LexicalSearch(q, reviveReach)
	if err != nil {
		p.ReviveErr = err
		return
	}
	// Each arm is read on its own rather than fused: a fused score cannot say
	// which arms a hit came from once the ranks run past RRF's constant, and
	// at this reach they do. An embedding that fails is reported as such and
	// the weighing goes on by keyword — saying "no model" would hide a broken
	// runtime the user configured.
	var vec map[string]bool
	if embed != nil && embedModel != "" {
		hits, err := ix.Search(embed, embedModel, q, reviveReach)
		if err != nil {
			p.ReviveEmbedErr = err
		} else {
			vec = map[string]bool{}
			for _, h := range hits {
				vec[h.Slug] = true
			}
		}
	}
	p.ReviveLexical = vec == nil
	asked := textmatch.Subject(req.Task + " " + c.Task + " " + c.Next)
	for w := range textmatch.Subject(p.scope() + " " + req.Hint) {
		delete(asked, w)
	}

	// The index slugs a note the way vault.Slug does — lowercased, "_convos"
	// read as "-convos" — so a hit's slug is not a path and not the slug
	// session gives the same checkpoint. Both sides go through vault.Slug
	// before they are compared, and the checkpoint itself is read from the
	// vault, never from the hit's body.
	key := func(slug string) string { return vault.Slug(ix.Vault, filepath.Join(ix.Vault, slug)) }
	shown := map[string]bool{key(c.Slug): true}
	rendered := map[string]bool{}
	for _, cp := range append([]session.Checkpoint{*c}, p.History...) {
		shown[key(cp.Slug)] = true
		for _, f := range cp.Failed {
			rendered[normalizeKey(f)] = true
		}
	}
	prefix := key(session.CheckpointDir+"/"+session.SafeScope(p.historyScope)) + "/"

	matched := map[string]bool{}
	for _, h := range lex {
		if h.Kind == "memory" || !strings.HasPrefix(h.Slug, prefix) || shown[h.Slug] {
			continue
		}
		if vec != nil && !vec[h.Slug] {
			continue
		}
		matched[h.Slug] = true
	}
	if len(matched) == 0 {
		return
	}

	type scored struct {
		Revived
		score float64
	}
	var picks []scored
	// Distinct rulings, not occurrences: a dead end recorded in three old
	// checkpoints is one approach weighed, and "3 checked" for it would
	// overstate how much the task was compared against.
	considered := map[string]bool{}
	for _, old := range earlier {
		if !matched[key(old.Slug)] || shown[key(old.Slug)] {
			continue
		}
		for _, f := range old.Failed {
			k := normalizeKey(f)
			if rendered[k] || considered[k] || session.IsPlaceholder(f) {
				continue
			}
			if retired, _ := reviews[session.ReviewKey(f)].For(old.TS, old.Git.Commit); retired {
				continue
			}
			considered[k] = true
			subject := textmatch.Subject(f)
			s := textmatch.Overlap(asked, subject)
			if s < textmatch.Related || (len(subject) >= 2 && shared(asked, subject) < 2) {
				continue
			}
			rendered[k] = true
			picks = append(picks, scored{Revived{Text: f, Agent: old.Agent, TS: old.TS, Slug: old.Slug, Commit: old.Git.Commit}, s})
		}
	}
	p.ReviveConsidered = len(considered)
	sort.SliceStable(picks, func(i, j int) bool {
		if picks[i].score != picks[j].score {
			return picks[i].score > picks[j].score
		}
		return picks[i].TS > picks[j].TS
	})
	for i := 0; i < len(picks) && i < maxRevived; i++ {
		p.Revived = append(p.Revived, picks[i].Revived)
	}
	p.markRevived(req.Dir, reviews)
}

// markRevived measures the revived rulings the way markRulings measures the
// rest, which ran before they existed. Without it the oldest rulings in the
// pack — the likeliest to have been overtaken by the code — were the only ones
// that never carried a warning.
func (p *Pack) markRevived(dir string, reviews map[string]session.Review) {
	if len(p.Revived) == 0 {
		return
	}
	a := gitstate.NewAnchors(dir)
	if a == nil {
		return
	}
	for _, r := range p.Revived {
		p.measureRuling(a, r.Text, r.TS, r.Commit, reviews)
	}
}

// shared counts the words of b that a holds in some form, as before_you_try
// does: containment alone lets a one-word overlap with a two-word ruling pass.
func shared(a, b map[string]bool) int {
	n := 0
	for w := range b {
		for v := range a {
			if textmatch.Akin(w, v) {
				n++
				break
			}
		}
	}
	return n
}
