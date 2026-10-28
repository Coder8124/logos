package contextpack

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/textmatch"
	"github.com/Coder8124/logos/internal/vault"
)

// Revived is a ruled-out approach from a checkpoint older than both the
// window and the handful History reaches, handed over because it bears on
// the task. It carries its own date so the render can say how old it is: a
// month-old ruling read as this week's is a dead end treated as fresh news.
type Revived struct {
	Text  string `json:"text"`
	Agent string `json:"agent,omitempty"`
	TS    int64  `json:"ts"`
	Slug  string `json:"slug,omitempty"`
}

// maxRevived bounds what relevance may bring back past the window. Three is
// a line each inside the checkpoint's fixed tier: enough for the one dead end
// that matters to arrive, too few for a vocabulary-heavy project to turn the
// window back into "everything ever ruled out".
const maxRevived = 3

// reviveReach is how many hits the relevance search asks for. Checkpoints
// compete with every note in the vault for these ranks, so it is wider than
// maxNotes, and still one indexed query.
const reviveReach = 30

// bothArms is the fused score a hit cannot reach from one arm alone: a single
// arm's best rank scores 1/(60+0), and any rank in both scores more while the
// reach stays under 60. Requiring it means the embedding and the keywords
// agreed the checkpoint is about this task, not just one of them guessing.
const bothArms = 1.0 / 60

// reviveOutOfWindow brings back older rulings the window would hide that the
// task reaches. A window is a guess about which period matters; a dead end
// does not expire with it, and the one ruled out two months ago on exactly
// this subject is the one an agent is most likely to walk back into.
//
// The checkpoints are found through the same index search and recall use, so
// relevance means what it means everywhere else, and they are read back from
// the vault, so a deleted index loses nothing here either. Two gates, both
// already in use: the hit must clear bothArms when there is an embedding
// model, and each ruling must match the task the way before_you_try matches a
// proposal by its words (textmatch.Related containment, two shared words).
// A ruling that only shares the project's name is not about the task.
func (p *Pack) reviveOutOfWindow(ix *index.Index, embed *provider.Provider, embedModel, query string, req Request, reviews map[string]session.Review) {
	c := p.Checkpoint
	if p.Window == nil || p.WindowEmpty || c == nil || p.historyScope == "" || ix.Vault == "" {
		return
	}
	embedded := embed != nil && embedModel != ""
	// The checkpoint's task and next step join the query for the reason they
	// join recall's: resume's own task is "resume work on X", which names no
	// subject at all.
	q := strings.TrimSpace(query + " " + c.Task + " " + c.Next)
	hits, err := ix.HybridSearch(embed, embedModel, q, reviveReach)
	if err != nil {
		return
	}
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

	matched := map[string]float64{}
	for _, h := range hits {
		if h.Kind == "memory" || !strings.HasPrefix(h.Slug, prefix) || shown[h.Slug] {
			continue
		}
		if embedded && h.Score <= bothArms {
			continue
		}
		matched[h.Slug] = h.Score
	}
	if len(matched) == 0 {
		p.ReviveLexical = !embedded
		return
	}
	// DecisionWindow is the depth "Decided earlier" already reads, so this
	// reaches no further back than the pack does for decisions.
	all, err := session.History(ix.Vault, p.historyScope, session.DecisionWindow)
	if err != nil {
		return
	}
	var found []session.Checkpoint
	for _, old := range all {
		if _, ok := matched[key(old.Slug)]; !ok || len(old.Failed) == 0 || p.Window.Contains(old.TS) {
			continue
		}
		found = append(found, old)
	}
	if req.Dir != "" && len(found) > 0 {
		found, _ = sameRepository(found, req.Dir)
	}

	type scored struct {
		Revived
		score float64
	}
	var picks []scored
	for _, old := range found {
		for _, f := range old.Failed {
			k := normalizeKey(f)
			if rendered[k] || session.IsPlaceholder(f) {
				continue
			}
			if retired, _ := reviews[session.ReviewKey(f)].For(old.TS, old.Git.Commit); retired {
				continue
			}
			p.ReviveConsidered++
			subject := textmatch.Subject(f)
			s := textmatch.Overlap(asked, subject)
			if s < textmatch.Related || (len(subject) >= 2 && shared(asked, subject) < 2) {
				continue
			}
			rendered[k] = true
			picks = append(picks, scored{Revived{Text: f, Agent: old.Agent, TS: old.TS, Slug: old.Slug}, s})
		}
	}
	p.ReviveLexical = !embedded
	sort.SliceStable(picks, func(i, j int) bool {
		if picks[i].score != picks[j].score {
			return picks[i].score > picks[j].score
		}
		return picks[i].TS > picks[j].TS
	})
	for i := 0; i < len(picks) && i < maxRevived; i++ {
		p.Revived = append(p.Revived, picks[i].Revived)
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
