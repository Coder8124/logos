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
	"strconv"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/graph"
	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/project"
	"github.com/Coder8124/brain/internal/provider"
	"github.com/Coder8124/brain/internal/secretary"
	"github.com/Coder8124/brain/internal/session"
	"github.com/Coder8124/brain/internal/when"
)

// A Request is what the caller is trying to do. Task is the important field:
// "continue the MCP implementation" retrieves differently from the project name
// alone, because it says which corner of the project matters right now.
type Request struct {
	Task string
	// Hint narrows the scope: a project name, a file path, or a topic. Optional
	// — if empty, the task text is matched against known projects.
	Hint string
	// Worktree is the linked git worktree the agent is standing in, empty in a
	// main checkout. It narrows continuity and nothing else: a worktree is the
	// same repository and so has the same memory, but its own uncommitted work
	// and its own place to have stopped.
	Worktree string
	// Budget is the approximate token ceiling for the rendered pack. 0 means
	// DefaultBudget.
	Budget int
	// Now is the clock the task's time expressions are resolved against. Zero
	// means the wall clock. It exists so "what was I working on last month" is
	// testable, and because a benchmark and a replay both need to ask the
	// question as of a moment that is not this one.
	Now int64
}

// A Pack is everything relevant to one task, gathered from every store, ready to
// be rendered into a model's context window.
type Pack struct {
	Task    string           `json:"task"`
	Hint    string           `json:"hint"`
	Project *project.Project `json:"project,omitempty"`
	// Worktree is the linked worktree continuity was read from, if any, and
	// Inherited says the checkpoint below came from the project rather than
	// from this worktree — a distinction the render has to make out loud.
	Worktree  string `json:"worktree,omitempty"`
	Inherited bool   `json:"inherited,omitempty"`

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

	Preferences []memory.Memory `json:"preferences"`
	Related     []memory.Memory `json:"related"`
	// Pinned holds memories the user marked always-include (see memory.Pin).
	// They bypass ranking entirely — see spendMemories — which is the whole
	// difference between a pin and simply having high salience.
	Pinned    []memory.Memory        `json:"pinned,omitempty"`
	OpenLoops []secretary.Commitment `json:"open_loops"`

	// Superseded holds recalled memories a later statement replaced. They are
	// kept out of the answer but reported, so the agent knows the value moved
	// rather than seeing one figure and assuming it was always so.
	Superseded []memory.Memory `json:"superseded,omitempty"`
	// Conflicts names sources that disagree and cannot be ordered. Unlike
	// supersession this resolves nothing — it says so out loud instead.
	Conflicts []string `json:"conflicts,omitempty"`

	// Window is the span of time the task asked about, when it asked about one,
	// and OutOfWindow counts what was set aside for falling outside it. The
	// count is rendered and the contents are not: an item excluded for being the
	// wrong date does not become less wrong for being listed, and reprinting it
	// would undo the filter. The number is there so the agent knows a filter ran
	// and can ask again without one.
	Window      *when.Window `json:"window,omitempty"`
	OutOfWindow int          `json:"out_of_window,omitempty"`
	// WindowEmpty is set when the window matched nothing dated, in which case the
	// filter was abandoned rather than applied and the pack holds everything it
	// gathered.
	WindowEmpty bool `json:"window_empty,omitempty"`

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
	p := Pack{Task: req.Task, Hint: req.Hint, Worktree: req.Worktree}
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
	//
	// Read from the worktree's scope when there is one, and only from the
	// project's when that turns up nothing. A worktree created this morning has
	// no checkpoint of its own, and the repository's last one is still the best
	// account of where the codebase stands — but it is the *project's*, so it is
	// marked inherited and the render says so. The moment the worktree writes
	// its own, this stops looking outside it, which is the whole point: two
	// parallel trees must not hand each other their stopping places.
	if scope := p.Continuity(); scope != "" && ix.Vault != "" {
		all, err := session.History(ix.Vault, scope, checkpointDepth)
		if (err != nil || len(all) == 0) && p.Worktree != "" {
			if all, err = session.History(ix.Vault, p.scope(), checkpointDepth); err == nil && len(all) > 0 {
				p.Inherited = true
			}
		}
		if err == nil && len(all) > 0 {
			p.Checkpoint = &all[0]
			p.History = all[1:]
		}
	}
	// Uncommitted notes get no such fallback. A checkpoint is a finished record
	// of where the codebase was; an open note is another agent's live work in
	// another tree, and presenting that as this session's own progress is
	// exactly the wrong handoff worktree scoping exists to prevent.
	if scope := p.Continuity(); scope != "" {
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
	// Pinned memories are not retrieved, they are asserted: fetched
	// unconditionally, independent of query and embedding model, because a
	// user who pinned something wants it present whether or not this
	// particular task happens to be relevant to it. No embed, no query, no
	// project resolved — none of that should be able to make a pin disappear.
	if pinned, err := memory.Pinned(db, p.scope()); err == nil {
		p.Pinned = pinned
	}
	p.Conflicts = contradictions(p.Notes)
	p.OpenLoops = openLoops(db, p.Project)
	p.applyWindow(req)

	return p, nil
}

// applyWindow narrows the pack to the span of time the task asked about.
//
// This is the one question ranked retrieval cannot answer by matching. Nobody
// writes "five weeks ago" in a note; they write what they did, on a day that
// turns out to be five weeks back, and only a clock connects the two. Without
// this the pack answers "what was I working on last month" with everything it
// has, newest first — which is not a bad answer to that question so much as an
// answer to a different one.
//
// Two rules keep a mis-parsed phrase from being destructive. Preferences are
// never filtered, because a preference is not an event: "I like proposals as
// bullet points" does not stop applying because it was recorded in June. And if
// the window empties every dated section, it is abandoned entirely and the pack
// says so — a question about a period with nothing in it should be told that,
// not handed a blank page.
func (p *Pack) applyWindow(req Request) {
	now := time.Now()
	if req.Now > 0 {
		now = time.Unix(req.Now, 0)
	}
	w, ok := when.Parse(req.Task, now)
	if !ok {
		return
	}

	notes := make([]index.Hit, 0, len(p.Notes))
	for _, h := range p.Notes {
		if w.Contains(h.FirstSeen) {
			notes = append(notes, h)
		}
	}
	working := make([]session.Note, 0, len(p.Working))
	for _, n := range p.Working {
		if w.Contains(n.TS) {
			working = append(working, n)
		}
	}
	related := make([]memory.Memory, 0, len(p.Related))
	for _, m := range p.Related {
		if w.Contains(m.Created) {
			related = append(related, m)
		}
	}

	dropped := (len(p.Notes) - len(notes)) + (len(p.Working) - len(working)) + (len(p.Related) - len(related))
	if dropped == 0 {
		p.Window = &w // nothing to filter, but the reader should still see what was assumed
		return
	}
	// Everything dated fell outside. Either the window is wrong or the period is
	// empty; either way, suppressing the whole pack would answer neither.
	if len(notes) == 0 && len(working) == 0 && len(related) == 0 {
		p.Window, p.WindowEmpty = &w, true
		return
	}

	p.Notes, p.Working, p.Related = notes, working, related
	p.Window, p.OutOfWindow = &w, dropped
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

// Continuity is the scope this pack's sessions and checkpoints are filed under:
// the project, narrowed to the worktree when the agent is standing in a linked
// one. Exported because a caller that wants to write into the same session — a
// note saying it resumed, say — has to key it the way the pack read it.
//
// A path segment rather than a joined name, because that is what it becomes in
// the vault: sessions/kestrel/feature-x/, a folder inside the project's rather
// than a sibling of it. A worktree is not another project — it is the same
// repository with a second working tree — and the layout should not claim
// otherwise. It also means a main checkout is untouched by any of this: its
// history is the files directly inside sessions/kestrel/, and a listing that
// skips directories cannot see the worktrees at all.
func (p Pack) Continuity() string {
	base := p.scope()
	w := strings.TrimSpace(p.Worktree)
	if base == "" || w == "" {
		return base
	}
	return base + "/" + w
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

// Carried is a one-line inventory of what this pack actually contains: the
// receipt a host shows the user so a restore is something they can see happen
// rather than something they have to take on faith.
//
// It counts and never characterises. "4 memories" is checkable against the
// text below it; "the important context" is not, and the moment a receipt says
// something the body does not support, every later receipt is worth less.
//
// Empty when the pack carries nothing, so a caller can tell "restored nothing"
// from "restored things I did not enumerate" and say the honest one.
func (p Pack) Carried() string {
	var parts []string
	add := func(n int, one, many string) {
		if n <= 0 {
			return
		}
		if n == 1 {
			parts = append(parts, "1 "+one)
			return
		}
		parts = append(parts, itoa(n)+" "+many)
	}
	if p.Checkpoint != nil {
		parts = append(parts, "the last checkpoint")
	}
	add(len(p.Working), "uncommitted note", "uncommitted notes")
	add(len(p.Notes), "note", "notes")
	add(len(p.Preferences)+len(p.Related), "memory", "memories")
	add(len(p.OpenLoops), "open loop", "open loops")
	// Worth naming separately: a superseded value that was *kept out* is the
	// single most distinctive thing this system does, and it is invisible in
	// the body precisely because it was excluded.
	add(len(p.Superseded), "stale value held back", "stale values held back")
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

func itoa(n int) string { return strconv.Itoa(n) }
