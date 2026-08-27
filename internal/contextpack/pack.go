// Package contextpack assembles everything needed to work on something into
// one bundle.
//
// It sits above the retrieval systems rather than beside them. memory, index,
// graph, secretary, and session each already answer one question well; what was
// missing was the thing that decides *which* of them to ask for a given task, in
// what proportion, and how to spend a fixed token budget across the answers.
// That judgement does not belong in the MCP server — which should stay an
// adapter — nor in internal/project, which would have to import half the repo.
//
// The distinction that matters: recall answers "what do you know about X", and
// this answers "give me what I need to do X". The second is not a longer version
// of the first. It includes where the last agent stopped, what they ruled out,
// what is still open, and the actual text of the notes involved — and it leaves
// things out on purpose, because the consumer has a finite context window.
package contextpack

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pragun/brain/internal/graph"
	"github.com/pragun/brain/internal/index"
	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/project"
	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/secretary"
	"github.com/pragun/brain/internal/session"
)

// A Request is what the caller is trying to do. Task is the important field:
// "continue the MCP implementation" retrieves differently from the project name
// alone, because it says which corner of the project matters right now.
type Request struct {
	Task string
	// Hint narrows the scope: a project name, a file path, or a topic. Optional
	// — if empty, the task text is matched against known projects.
	Hint string
	// Budget is the approximate token ceiling for the rendered pack. 0 means
	// DefaultBudget.
	Budget int
}

// A Pack is everything relevant to one task, gathered from every store, ready to
// be rendered into a model's context window.
type Pack struct {
	Task    string           `json:"task"`
	Hint    string           `json:"hint"`
	Project *project.Project `json:"project,omitempty"`

	// Checkpoint is where the last agent stopped on this project, and Working is
	// what has been recorded since without being committed. Together they are the
	// answer to "where were we", which no amount of semantic recall provides.
	Checkpoint *session.Checkpoint `json:"checkpoint,omitempty"`
	Working    []session.Note      `json:"working,omitempty"`
	// History is earlier checkpoints on the same project. Only their ruled-out
	// approaches are used — see Build.
	History []session.Checkpoint `json:"history,omitempty"`

	// Notes is vault content — the actual prose, not just titles. Hits reached
	// through the graph rather than matched directly carry Via.
	Notes []index.Hit `json:"notes"`

	Preferences []memory.Memory        `json:"preferences"`
	Related     []memory.Memory        `json:"related"`
	OpenLoops   []secretary.Commitment `json:"open_loops"`

	// Superseded holds recalled memories a later statement replaced. They are
	// kept out of the answer but reported, so the agent knows the value moved
	// rather than seeing one figure and assuming it was always so.
	Superseded []memory.Memory `json:"superseded,omitempty"`
	// Conflicts names sources that disagree and cannot be ordered. Unlike
	// supersession this resolves nothing — it says so out loud instead.
	Conflicts []string `json:"conflicts,omitempty"`

	// Sources lists what actually made it into the render, so the consumer can
	// cite rather than paraphrase.
	Sources []string `json:"sources"`
	// Excluded names what the budget dropped. An agent that can see what it did
	// not get can ask for more, which is strictly better than not knowing.
	Excluded []string `json:"excluded"`
	Budget   Budget   `json:"budget"`
}

const (
	maxPrefs   = 8
	maxRelated = 10
	maxNotes   = 12
	maxListed  = 8
	// checkpointDepth is how far back continuity reaches: the current
	// checkpoint plus a few predecessors' dead ends. Deep enough for a chain of
	// handoffs, shallow enough that a long-running project does not spend its
	// whole budget on archaeology.
	checkpointDepth = 4
)

// Build gathers candidates from every store. It deliberately over-gathers: what
// survives is decided at render time by the budget, because how much fits
// depends on how long each piece turns out to be.
//
// Everything here degrades rather than fails. A vault with no project notes, no
// embedding model, or no checkpoints still produces a useful pack — a memory
// layer that errors because you asked about something it doesn't know is worse
// than useless, it is untrustworthy.
func Build(ix *index.Index, embed *provider.Provider, embedModel string, req Request) (Pack, error) {
	db := ix.DB
	p := Pack{Task: req.Task, Hint: req.Hint}
	p.Budget.Limit = req.Budget
	if p.Budget.Limit <= 0 {
		p.Budget.Limit = DefaultBudget
	}

	p.Project = resolve(db, req)

	// The query both retrieval arms see. Task first: it carries the intent, and
	// the project name alone would pull the project's centre of mass rather than
	// the corner being worked on.
	query := strings.TrimSpace(req.Task + " " + req.Hint)
	if p.Project != nil {
		query = strings.TrimSpace(query + " " + p.Project.Name)
	}

	// Where the last agent stopped. Read from the vault, so it works even on a
	// freshly rebuilt index.
	//
	// Scoped by the hint when no project note resolved, because continuity must
	// not depend on the vault having been curated. An agent working through MCP
	// may leave checkpoints on a project that never got a note of its own — and
	// the checkpoint is itself the evidence that the project exists.
	// The latest checkpoint, and what earlier ones ruled out.
	//
	// History matters because dead ends accumulate across handoffs, not within
	// them. On the third agent of a chain, the approach the *first* one killed
	// is still dead and still expensive to rediscover — and it lives in a file
	// that agent never wrote and the current checkpoint does not mention. Only
	// the failures are carried forward: the rest of an old checkpoint is stale
	// narration, but "we tried this and it did not work" does not expire.
	if scope := p.scope(); scope != "" && ix.Vault != "" {
		if all, err := session.History(ix.Vault, scope, checkpointDepth); err == nil && len(all) > 0 {
			p.Checkpoint = &all[0]
			p.History = all[1:]
		}
	}
	if scope := p.scope(); scope != "" {
		if notes, err := session.Uncommitted(db, scope); err == nil {
			p.Working = notes
		}
	}

	// Vault prose. This is the piece context_pack never had: it listed a
	// project's files but never the content, so an agent still had to go read
	// them one by one.
	if embed != nil && query != "" {
		if hits, err := ix.HybridSearch(embed, embedModel, query, maxNotes); err == nil {
			p.Notes = withoutSessionNotes(hits)
		}
	}
	p.Notes = interleave(p.Notes, p.graphReach(db))

	if prefs, err := memory.Surface(db, []memory.Kind{memory.Preference}, maxPrefs); err == nil {
		for _, m := range prefs {
			if m.Project == "" { // global preferences apply everywhere
				p.Preferences = append(p.Preferences, m)
			}
		}
	}
	if embed != nil && query != "" {
		if mems, err := memory.Recall(db, embed, embedModel, query, maxRelated); err == nil {
			p.Related, p.Superseded = supersede(req.Task, mems)
		}
	}
	p.Conflicts = contradictions(p.Notes)
	p.OpenLoops = openLoops(db, p.Project)

	return p, nil
}

// withoutSessionNotes keeps checkpoints out of the vault-prose arm.
//
// Checkpoints are markdown in the vault, which is what makes them durable — and
// also means ordinary retrieval finds them, as files, with no idea what they
// are. Two things went wrong when it did.
//
// Another project's checkpoint came back as ordinary context: asked to resume
// one piece of work, the search arm cheerfully supplied a different one's
// ruled-out approaches, and nothing told the agent they belonged elsewhere.
//
// Worse, *this* project's checkpoint came back too — the same file already
// rendered above, but raw. That quietly undid the reasoning applied to it:
// a next step withdrawn by a later decision was suppressed in the checkpoint
// section and then printed verbatim two sections down, straight out of the
// file. Retrieval had no way to know the plan had been called off.
//
// So the whole directory is excluded here. Continuity has its own section and
// its own rules; letting the raw file in through a second door bypasses them.
func withoutSessionNotes(hits []index.Hit) []index.Hit {
	out := hits[:0]
	for _, h := range hits {
		if strings.HasPrefix(h.Slug, session.CheckpointDir+"/") {
			continue
		}
		out = append(out, h)
	}
	return out
}

// scope is the name continuity is filed under.
//
// It is the project's trailing segment ("kestrel-one"), not its full slug
// ("projects/kestrel-one"), because that is the name an agent passes to
// checkpoint and the name edges resolve against. Filing work under the folder
// path would mean a checkpoint written before the project note existed could
// never be found after it did.
func (p Pack) scope() string {
	if p.Project != nil {
		slug := p.Project.Slug
		if i := strings.LastIndex(slug, "/"); i >= 0 {
			return slug[i+1:]
		}
		return slug
	}
	return strings.TrimSpace(p.Hint)
}

// graphReach pulls in notes one hop from the project that retrieval did not
// already find.
//
// This is the case ranked retrieval structurally misses: a note that never
// mentions the query's words but that the project explicitly depends on. On the
// demo vault, asking about the bill of materials would not surface the yield
// note, even though the yield is what the cost hinges on — the vault says so in
// an edge, and an edge is a fact the user asserted.
func (p Pack) graphReach(db *sql.DB) []index.Hit {
	if p.Project == nil {
		return nil
	}
	g, err := graph.Ego(db, p.Project.Slug, 1, false)
	if err != nil {
		return nil
	}
	have := map[string]bool{p.Project.Slug: true}
	for _, h := range p.Notes {
		have[h.Slug] = true
	}

	ix := &index.Index{DB: db}
	var out []index.Hit
	for _, n := range g.Nodes {
		if n.Hops == 0 || have[n.Slug] {
			continue
		}
		// Checkpoints arrive through their own section; pulling them in here
		// would print the same work twice.
		if n.Kind == "checkpoint" {
			continue
		}
		h, ok := ix.HitBySlug(n.Slug)
		if !ok {
			continue
		}
		h.Via = p.Project.Slug
		out = append(out, h)
		have[n.Slug] = true
	}
	// Deterministic order; the budget decides how many survive.
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// certainDirect is how many top-ranked search hits are placed ahead of the
// graph's contribution.
const certainDirect = 3

// interleave decides the order the budget will consume notes in, and therefore
// which notes actually reach the agent.
//
// Appending graph hits after the direct ones seems natural and is wrong: they
// end up last in the queue, the budget runs out on the eighth semantic match,
// and the one note ranked retrieval structurally could not find is the one that
// gets dropped. That is the exact failure this feature exists to prevent.
//
// So the top few direct hits go first — those are certainly relevant — and then
// the graph's notes, ahead of the long tail of weaker matches. A note the user
// explicitly linked to this project outranks the eighth-best guess about what
// they meant.
func interleave(direct, viaGraph []index.Hit) []index.Hit {
	if len(viaGraph) == 0 {
		return direct
	}
	head := direct
	var tail []index.Hit
	if len(direct) > certainDirect {
		head, tail = direct[:certainDirect], direct[certainDirect:]
	}
	out := make([]index.Hit, 0, len(direct)+len(viaGraph))
	out = append(out, head...)
	out = append(out, viaGraph...)
	return append(out, tail...)
}

// openLoops returns commitments still outstanding, those touching this project
// first. An agent picking up work should know what was promised before it
// decides what to do next.
func openLoops(db *sql.DB, p *project.Project) []secretary.Commitment {
	loops, err := secretary.Open_(db)
	if err != nil {
		return nil
	}
	if p == nil {
		return loops
	}
	terms := append([]string{p.Name, p.Slug}, p.Aliases...)
	var mine, rest []secretary.Commitment
	for _, c := range loops {
		if mentionsAny(c.Text, terms) {
			mine = append(mine, c)
		} else {
			rest = append(rest, c)
		}
	}
	return append(mine, rest...)
}

func mentionsAny(text string, terms []string) bool {
	lower := strings.ToLower(text)
	for _, t := range terms {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

// resolve maps a request to a project: the explicit hint first, then a file the
// project has touched, then the file's parent directory, and finally the task
// text itself — an agent that says "continue the MCP server work" has named its
// project without knowing it.
func resolve(db *sql.DB, req Request) *project.Project {
	if h := strings.TrimSpace(req.Hint); h != "" {
		if p, ok, err := project.Get(db, h); err == nil && ok {
			return &p
		}
	}

	projects, err := project.Detect(db)
	if err != nil {
		return nil // no project layer available: degrade gracefully
	}

	if h := strings.TrimSpace(req.Hint); h != "" {
		base := strings.ToLower(filepath.Base(h))
		for _, p := range projects { // most-recently-active first
			for _, f := range p.Files {
				if strings.EqualFold(f.Path, h) || strings.ToLower(filepath.Base(f.Path)) == base {
					pp := p
					return &pp
				}
			}
		}
		if dir := filepath.Base(filepath.Dir(h)); dir != "" && dir != "." && dir != string(filepath.Separator) {
			if p, ok, err := project.Get(db, dir); err == nil && ok {
				return &p
			}
		}
	}

	if task := strings.TrimSpace(req.Task); task != "" {
		for _, p := range projects {
			if mentionsAny(task, append([]string{p.Name, p.Slug}, p.Aliases...)) {
				pp := p
				return &pp
			}
		}
	}
	return nil
}
