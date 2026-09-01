package contextpack

import (
	"fmt"
	"strings"
	"time"

	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/project"
	"github.com/pragun/brain/internal/secretary"
)

// Rendering is where the budget is actually spent, which is why Render takes a
// pointer: deciding what fits is not a formatting detail, it is the last step of
// assembly, and the pack records afterwards what it kept, what it dropped, and
// what it cost.
//
// Allocation order and reading order are deliberately different. The checkpoint
// leads the page because it is what a resuming agent needs first, but the
// budget is handed out with vault prose *last* — every other section is a
// handful of one-line summaries with a natural size, whereas notes are elastic
// and will absorb whatever they are given. Spending in reading order meant the
// pack routinely returned two thousand tokens of a four thousand token budget
// while still reporting notes it had dropped, which is the worst of both.

// Render writes the pack as markdown for a model to read. It fills in Budget,
// Sources, and Excluded as a side effect.
func (p *Pack) Render() string {
	p.Excluded = nil
	p.Sources = nil

	// 1. Gather each section's candidates.
	working := p.wantWorking()
	proj := p.wantProject()
	mems := p.wantMemories()
	loops := p.wantLoops()
	notes, noteSlugs := p.wantNotes()

	// 2. Spend, smallest and most fixed first, so the elastic section inherits
	//    everything the others did not need.
	sp := newSpender(p.Budget.Limit)
	checkpoint := p.spendCheckpoint(sp)
	keptWorking := sp.take(secWorking, working)
	keptProj := sp.take(secProject, proj)
	keptMems := sp.take(secMemories, mems)
	keptLoops := sp.take(secLoops, loops)
	keptNotes := sp.take(secNotes, notes)

	// 3. Render in the order an agent should read it.
	var b strings.Builder
	p.renderHeader(&b)
	p.renderWindow(&b)
	p.renderCheckpoint(&b, checkpoint)
	p.renderWorking(&b, keptWorking)
	section(&b, "The project", keptProj, "\n")
	section(&b, "From the vault", keptNotes, "\n\n")
	section(&b, "What you've told me", keptMems, "\n")
	section(&b, "Still open", keptLoops, "\n")
	p.renderConflicts(&b)
	p.renderGaps(&b, keptWorking, keptProj, keptNotes, keptMems)

	// Sources and exclusions, now that what survived is known.
	if p.Project != nil && len(keptProj) > 0 {
		p.Sources = append(p.Sources, p.Project.Slug)
	}
	p.Sources = append(p.Sources, noteSlugs[:len(keptNotes)]...)
	for _, s := range noteSlugs[len(keptNotes):] {
		p.Excluded = append(p.Excluded, s+" (budget)")
	}
	p.noteDropped(secWorking, len(working)-len(keptWorking))
	p.noteDropped(secProject, len(proj)-len(keptProj))
	p.noteDropped(secMemories, len(mems)-len(keptMems))
	p.noteDropped(secLoops, len(loops)-len(keptLoops))

	p.renderSources(&b)
	p.Budget.Spent = sp.spent
	p.Budget.By = sp.lines
	p.renderBudget(&b)
	return b.String()
}

func (p *Pack) renderHeader(b *strings.Builder) {
	if p.Task != "" {
		fmt.Fprintf(b, "# Context for: %s\n", oneLine(p.Task))
	} else {
		fmt.Fprintf(b, "# Context for %s\n", p.Hint)
	}
	switch {
	case p.Project != nil:
		fmt.Fprintf(b, "\nProject **%s** — last active %s.\n",
			p.Project.Name, project.Age(p.Project.LastActive))
	case p.Checkpoint != nil:
		// No project note, but there is a record of work. Saying "no project
		// matched" here would tell the agent to disregard the very thing it is
		// about to be handed.
		fmt.Fprintf(b, "\nProject **%s** — no note in the vault yet, but there is a work history below.\n",
			p.scope())
	default:
		hint := p.Hint
		if hint == "" {
			hint = p.Task
		}
		fmt.Fprintf(b, "\n_No project matched %q — standing context only._\n", oneLine(hint))
	}
	// Stated once, at the top, where it frames everything under it. See
	// untrusted.go for why structure alone is not enough.
	fmt.Fprintf(b, "\n%s\n", boundary)
}

// renderWindow states the time filter that ran, and what it cost.
//
// A filter nobody can see is a filter nobody can correct. The phrase is echoed
// with the dates it resolved to, so an asker who meant a different fortnight can
// tell at a glance; the count of what was set aside tells the agent there is
// more behind the filter and that asking again without a date would get it.
//
// What is deliberately absent is the content of what was excluded. Naming those
// items would put the out-of-period material back in the context window, which
// is the entire thing the filter was for.
func (p *Pack) renderWindow(b *strings.Builder) {
	if p.Window == nil {
		return
	}
	switch {
	case p.WindowEmpty:
		fmt.Fprintf(b, "\n_You asked about %s. Nothing recorded falls in that period, so everything below is unfiltered — read the dates before trusting any of it as an answer._\n",
			p.Window)
	case p.OutOfWindow > 0:
		fmt.Fprintf(b, "\n_Filtered to %s — %d %s outside that period %s set aside. Ask without a date to see %s._\n",
			p.Window, p.OutOfWindow, plural(p.OutOfWindow, "item", "items"),
			plural(p.OutOfWindow, "was", "were"), plural(p.OutOfWindow, "it", "them"))
	default:
		fmt.Fprintf(b, "\n_Filtered to %s. Everything recorded falls inside it._\n", p.Window)
	}
}

// spendCheckpoint charges the checkpoint against its share and returns the body
// to print. It is a single blob rather than a list, so it is trimmed rather than
// truncated by item count.
func (p *Pack) spendCheckpoint(sp *spender) string {
	c := p.Checkpoint
	if c == nil {
		sp.skip(secCheckpoint)
		return ""
	}
	// The checkpoint is budgeted in two tiers, because one of its fields has no
	// natural size and the rest do.
	//
	// State absorbs every uncommitted working note at commit time, so on a busy
	// project it is unbounded — forty standup lines of "no blockers". The
	// decisions, the ruled-out approaches, the open questions and the next step
	// are a handful of lines each and are the only part of a checkpoint that
	// cannot be reconstructed from anything else.
	//
	// Trimming the body as one blob cut from the bottom, which meant routine
	// chatter in State evicted "already tried, didn't work" — the single most
	// valuable section in the pack, and the whole reason checkpoints exist. So
	// the irreplaceable tiers are charged first and State spends what is left.
	var head, tail strings.Builder
	if c.Task != "" {
		fmt.Fprintf(&head, "**They were doing:** %s\n\n", inline(c.Task))
	}
	list(&tail, "Decided", c.Decisions)
	// The most valuable lines in the pack: what has already been ruled out.
	list(&tail, "Already tried, didn't work", c.Failed)
	list(&tail, "Still open", c.Questions)
	list(&tail, "Files touched", c.Files)
	// Predecessors' dead ends, attributed. Cheap — a line each — and the only
	// thing in an old checkpoint that is still true.
	if older := p.priorFailures(); len(older) > 0 {
		list(&tail, "Ruled out earlier in this project", older)
	}
	if c.Next != "" {
		if m, ok := overtaken(c.Next, c.TS, p.all()); ok {
			// The plan is not printed. An agent handed "next step: X" acts on X,
			// and a caveat underneath does not reliably stop it — so the line
			// that would send it back into abandoned work is replaced by the
			// statement that abandoned it.
			fmt.Fprintf(&tail, "**Next step:** _the plan recorded here was overtaken — %s_\n",
				oneLine(m.Text))
			p.Excluded = append(p.Excluded, "the checkpoint's next step (superseded by a later decision)")
		} else {
			fmt.Fprintf(&tail, "**Next step:** %s\n", inline(c.Next))
		}
	}

	allowance := sp.allowance(secCheckpoint)
	fixed := head.String() + tail.String()
	state, trimmed := "", false
	if s := strings.TrimSpace(c.State); s != "" {
		room := allowance - estimate(fixed)
		if room <= 0 {
			trimmed = true
		} else if state, trimmed = fit(s, room); state != "" {
			state = block(state) + "\n\n"
		}
	}
	if trimmed {
		p.Excluded = append(p.Excluded, "part of the checkpoint's session log (budget)")
	}

	text := head.String() + state + tail.String()
	cost := estimate(text)
	sp.spent += cost
	sp.carry = max(0, allowance-cost)
	dropped := 0
	if trimmed {
		dropped = 1
	}
	sp.lines = append(sp.lines, Line{Section: secCheckpoint, Tokens: cost, Items: 1, Dropped: dropped})
	if c.Slug != "" {
		p.Sources = append(p.Sources, c.Slug)
	}
	return text
}

// renderCheckpoint leads the pack: it is the one section nothing else can
// reconstruct. Everything below it is retrievable; this is testimony.
func (p *Pack) renderCheckpoint(b *strings.Builder, body string) {
	c := p.Checkpoint
	if c == nil || body == "" {
		return
	}
	who := c.Agent
	if who == "" {
		who = "an agent"
	}
	fmt.Fprintf(b, "\n## Where we left off\n\nLast checkpoint by **%s**, %s", inline(who), project.Age(c.TS))
	if c.HandoffTo != "" {
		fmt.Fprintf(b, ", handed off to **%s**", inline(c.HandoffTo))
	}
	b.WriteString(".")
	// What the repository was, as distinct from what the agent said about it.
	// An arriving agent can check this out and be standing where the decisions
	// were made, which is the difference between "this was true then" and "this
	// was true at a3f9c2".
	if s := c.Git.Summary(); s != "" {
		fmt.Fprintf(b, "\n\n**Repository was:** %s", s)
		if c.Git.Subject != "" {
			fmt.Fprintf(b, " — %q", oneLine(c.Git.Subject))
		}
		if c.Git.Worktree != "" {
			// Same project, divergent parallel state, which is exactly where a
			// handoff goes wrong silently.
			fmt.Fprintf(b, "\n**In worktree:** %s", c.Git.Worktree)
		}
	}
	// A checkpoint that has sat for a fortnight describes a situation that may
	// have moved. Saying so costs a clause and stops the next agent treating a
	// stale plan as the current one.
	if daysOld(c.TS) >= staleAfterDays {
		fmt.Fprintf(b, " That was %s — treat the plan below as a starting point, not the current state.", humanAge(c.TS))
	}
	fmt.Fprintf(b, "\n\n%s\n", body)
}

func (p *Pack) wantWorking() []string {
	want := make([]string, 0, len(p.Working))
	multi := len(p.workingAuthors()) > 1
	// The section header carries the age of the *oldest* note, which is the
	// right warning to lead with and quietly wrong about every other line when
	// the notes are spread out. A run of notes covering six weeks reads as six
	// weeks old throughout, so once they span more than a day each one says when
	// it was written.
	spread := p.workingSpansDays()
	for _, n := range p.Working {
		// Attribute when more than one agent contributed. Which of them found a
		// thing is part of the finding: an agent that inherits "the vendor says
		// it cannot be tuned" without knowing who established that cannot judge
		// how hard to lean on it. With a single author the name goes in the
		// heading instead, because repeating it on every line is noise.
		line := "- "
		if spread {
			if d := shortDate(n.TS); d != "" {
				line += d + " — "
			}
		}
		if multi && n.Agent != "" {
			line += "(" + inline(n.Agent) + ") "
		}
		// Deliberately not clipped to a line length. A working note is a
		// finding an agent chose to write down — often the reason a whole
		// approach was abandoned — and truncating it mid-sentence destroys
		// exactly the part that was worth keeping. If it does not fit, the
		// budget should say so; a hard character cap says nothing.
		want = append(want, line+flatten(n.Text))
	}
	return want
}

// workingAuthors lists the distinct agents that left uncommitted notes, oldest
// contribution first.
func (p *Pack) workingAuthors() []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range p.Working {
		if n.Agent != "" && !seen[n.Agent] {
			seen[n.Agent] = true
			out = append(out, n.Agent)
		}
	}
	return out
}

// renderWorking prints uncommitted work with the two things that decide how far
// to trust it: who wrote it, and how old it is.
//
// Age is the one this benchmark was built around. Uncommitted notes carry
// urgency in their wording — "about sixteen days before the freeze" — and
// nothing in the note says when that was true. Handed over undated, a fortnight
// later, that sentence is an active lie about a schedule. Every memory system
// tested had this failure; it is cheap to fix and nobody does it.
func (p *Pack) renderWorking(b *strings.Builder, items []string) {
	if len(items) == 0 {
		return
	}
	b.WriteString("\n## Recorded since, not yet checkpointed\n")

	var meta []string
	if authors := p.workingAuthors(); len(authors) == 1 {
		meta = append(meta, "by **"+authors[0]+"**")
	} else if len(authors) > 1 {
		meta = append(meta, "by **"+strings.Join(authors, "**, **")+"**")
	}
	if oldest := p.oldestWorking(); oldest > 0 {
		age := humanAge(oldest)
		if daysOld(oldest) >= staleAfterDays {
			age += " — this may be out of date, check anything time-sensitive before acting on it"
		}
		meta = append(meta, age)
	}
	if len(meta) > 0 {
		fmt.Fprintf(b, "\n_%s. Never checkpointed._\n", strings.Join(meta, ", "))
	}
	b.WriteString("\n" + strings.Join(items, "\n") + "\n")
}

// priorFailures collects what earlier checkpoints ruled out, attributed to
// whoever ruled it out and skipping anything the current checkpoint already
// repeats.
func (p *Pack) priorFailures() []string {
	seen := map[string]bool{}
	if p.Checkpoint != nil {
		for _, f := range p.Checkpoint.Failed {
			seen[normalizeKey(f)] = true
		}
	}
	var out []string
	for _, c := range p.History {
		for _, f := range c.Failed {
			k := normalizeKey(f)
			if seen[k] {
				continue
			}
			seen[k] = true
			who := c.Agent
			if who == "" {
				who = "an earlier agent"
			}
			out = append(out, fmt.Sprintf("(%s) %s", who, flatten(f)))
		}
	}
	return out
}

func normalizeKey(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// all is every memory the pack is carrying, preferences included.
func (p *Pack) all() []memory.Memory {
	return append(append([]memory.Memory{}, p.Related...), p.Preferences...)
}

func (p *Pack) oldestWorking() int64 {
	var oldest int64
	for _, n := range p.Working {
		if n.TS > 0 && (oldest == 0 || n.TS < oldest) {
			oldest = n.TS
		}
	}
	return oldest
}

// workingSpansDays reports whether the uncommitted notes cover more than a
// single day, which is when one age for the whole section stops describing any
// particular line in it.
func (p *Pack) workingSpansDays() bool {
	var oldest, newest int64
	for _, n := range p.Working {
		if n.TS <= 0 {
			continue
		}
		if oldest == 0 || n.TS < oldest {
			oldest = n.TS
		}
		if n.TS > newest {
			newest = n.TS
		}
	}
	return oldest > 0 && newest-oldest > 86400
}

func (p *Pack) wantProject() []string {
	pr := p.Project
	if pr == nil {
		return nil
	}
	var want []string
	for _, g := range clip(pr.Goals, 6) {
		want = append(want, "- goal: "+oneLine(g))
	}
	for i, item := range pr.Progress {
		if i >= maxListed {
			break
		}
		want = append(want, fmt.Sprintf("- %s: %s", project.Age(item.TS), oneLine(item.Text)))
	}
	if len(pr.People) > 0 {
		var names []string
		for _, r := range pr.People {
			names = append(names, r.Title)
		}
		want = append(want, "- people: "+strings.Join(names, ", "))
	}
	for i, m := range pr.Memories {
		if i >= maxListed {
			break
		}
		want = append(want, fmt.Sprintf("- known: (%s) %s", m.Kind, oneLine(m.Text)))
	}
	return want
}

// wantNotes returns vault prose chunks and their slugs in the same order. Every
// chunk carries its slug so the consumer can cite the note rather than restate
// it as if it were its own knowledge.
func (p *Pack) wantNotes() ([]string, []string) {
	want := make([]string, 0, len(p.Notes))
	slugs := make([]string, 0, len(p.Notes))
	for _, h := range p.Notes {
		head := fmt.Sprintf("### %s  `%s`", inline(h.Title), inline(h.Slug))
		if h.Via != "" {
			head += fmt.Sprintf("\n_reached through the graph from %s — not a direct match_", inline(h.Via))
		}
		want = append(want, head+"\n\n"+block(strings.TrimSpace(h.Body)))
		slugs = append(slugs, h.Slug)
	}
	return want, slugs
}

// wantMemories renders what the assistant has been told, with the provenance
// that decides how much to trust it. A preference stated by hand and one
// inferred from a passing remark should not read identically.
//
// Facts carry the date they were recorded, because a great many of them are
// written relative to the moment of writing. "Signed the manufacturing
// agreement today" is a complete statement when said and an unanswerable one a
// month later: three such facts, recorded weeks apart, arrive as three
// identical claims about today and the reader cannot order them. The date is
// what makes the sentence mean again what it meant when it was written.
//
// Preferences do not get one. A preference is not an event — "bring me
// proposals as bullet points" is as true now as when it was said — and dating it
// invites a reader to discount it for age.
func (p *Pack) wantMemories() []string {
	var want []string
	seen := map[int64]bool{}
	for _, m := range p.Preferences {
		want = append(want, fmt.Sprintf("- preference: %s  _(%s)_", oneLine(m.Text), inline(m.Source)))
		seen[m.ID] = true
	}
	for _, m := range p.Related {
		if seen[m.ID] {
			continue
		}
		meta := inline(m.Source)
		if d := shortDate(m.Created); d != "" {
			meta += ", " + d
		}
		want = append(want, fmt.Sprintf("- %s: %s  _(%s, confidence %.2f)_",
			m.Kind, oneLine(m.Text), meta, m.Confidence))
	}
	return want
}

func (p *Pack) wantLoops() []string {
	want := make([]string, 0, len(p.OpenLoops))
	for _, c := range p.OpenLoops {
		want = append(want, "- "+loopLine(c))
	}
	return want
}

func loopLine(c secretary.Commitment) string {
	s := oneLine(c.Text)
	if c.Who != "" {
		s += " (" + c.Who + ")"
	}
	if c.DueHint != "" {
		s += " — due " + c.DueHint
	}
	return s
}

func section(b *strings.Builder, heading string, items []string, sep string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## %s\n\n", heading)
	b.WriteString(strings.Join(items, sep) + "\n")
}

// renderConflicts reports disagreement rather than resolving it.
//
// Supersession is a different case and is handled by dropping the older value:
// where two statements about the same thing are separated in time, the later
// one is the answer. Here there is no ordering to lean on, so both stay and the
// agent is told to check. Silently picking one is the failure mode this exists
// to prevent — it produces a confident answer with a citation attached, which
// is the hardest kind of wrong to catch.
func (p *Pack) renderConflicts(b *strings.Builder) {
	if len(p.Conflicts) == 0 && len(p.Superseded) == 0 {
		return
	}
	b.WriteString("\n## Not settled\n\n")
	for _, c := range p.Conflicts {
		fmt.Fprintf(b, "- **Sources conflict** — %s. Both are in the vault; neither supersedes the other.\n", c)
	}
	// Deliberately without the superseded text.
	//
	// Reprinting it to be transparent put the dead value back into the context
	// window, which is the entire thing suppression exists to prevent — the
	// agent reads "$199" and has no reason to care that a caption called it
	// superseded. What the agent needs is that the figure moved, so it does not
	// treat one number as though it had always been true; the old value itself
	// is history, and history is available on request.
	if n := len(p.Superseded); n > 0 {
		oldest := p.Superseded[0].Created
		for _, m := range p.Superseded {
			if m.Created < oldest {
				oldest = m.Created
			}
		}
		fmt.Fprintf(b, "- This changed: %d earlier %s superseded by what is above, going back to %s. "+
			"The values shown are the current ones; ask for the history if the change matters.\n",
			n, plural(n, "statement was", "statements were"), project.Age(oldest))
	}
}

// renderGaps says when the store has nothing, instead of letting a neighbouring
// fact stand in as an answer.
//
// This is the failure every system benchmarked shares, and the one with the
// worst consequence. Asked about a warranty period it never recorded, a store
// returns the manufacturing agreement — topically adjacent, retrieved with a
// respectable score, and not an answer. The agent has no way to tell the
// difference between "here is what you asked for" and "here is the nearest
// thing I had", so it answers the user from the nearest thing.
//
// Note the asymmetry with the rest of the pack: everywhere else the cost of
// being wrong is a wasted section, and here it is a fabricated answer. So this
// fires on weak retrieval rather than on empty retrieval.
func (p *Pack) renderGaps(b *strings.Builder, working, proj, notes, mems []string) {
	if p.Checkpoint != nil || len(working) > 0 || len(proj) > 0 {
		return // there is real work here; the question is not unanswered
	}
	if len(notes) > 0 {
		return // vault prose is substantive enough to stand as an answer
	}

	// Getting memories back means retrieval found *something*; it does not mean
	// it found what was asked for. answered() is what separates the two.
	if len(mems) > 0 && answered(p.Task, p.all()) {
		return
	}
	switch {
	case len(mems) == 0:
		b.WriteString("\n## Nothing recorded\n\n")
		fmt.Fprintf(b, "_There is no record bearing on %q. Nothing was found — say so rather than inferring an answer from context._\n",
			oneLine(p.Task))
	default:
		b.WriteString("\n## Possibly not recorded\n\n")
		fmt.Fprintf(b, "_Nothing directly answers %q. What follows above was retrieved as the nearest related material and may not be an answer — check before treating it as one._\n",
			oneLine(p.Task))
	}
}

func (p *Pack) renderSources(b *strings.Builder) {
	if len(p.Sources) == 0 {
		return
	}
	p.Sources = dedup(p.Sources)
	b.WriteString("\n## Sources\n\n")
	for _, s := range p.Sources {
		fmt.Fprintf(b, "- %s\n", s)
	}
}

// renderBudget closes the pack with what it cost and what it left out. An agent
// that can see the shortfall can ask for a bigger budget; one that cannot will
// simply assume it received everything.
func (p *Pack) renderBudget(b *strings.Builder) {
	fmt.Fprintf(b, "\n---\n_Context budget: ~%d of %d tokens", p.Budget.Spent, p.Budget.Limit)
	var parts []string
	for _, l := range p.Budget.By {
		if l.Tokens > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", l.Section, l.Tokens))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(b, " (%s)", strings.Join(parts, ", "))
	}
	b.WriteString("._\n")

	if len(p.Excluded) > 0 {
		p.Excluded = dedup(p.Excluded)
		b.WriteString("\n_Left out for space — ask with a larger budget if you need them:_\n")
		for _, e := range p.Excluded {
			fmt.Fprintf(b, "- %s\n", e)
		}
	}
}

func (p *Pack) noteDropped(section string, n int) {
	if n > 0 {
		p.Excluded = append(p.Excluded, fmt.Sprintf("%d more %s (budget)", n, section))
	}
}

// list writes a labelled group of single-value fields — decisions, dead ends,
// open questions.
func list(b *strings.Builder, heading string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "**%s:**\n", heading)
	for _, it := range items {
		// inline, not TrimSpace: these are single-value fields, and a newline
		// inside one is how payload forges a heading. See untrusted.go.
		fmt.Fprintf(b, "- %s\n", inline(it))
	}
	b.WriteString("\n")
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// staleAfterDays is when context stops being reported as simply current.
//
// A week is short enough to catch a moving schedule and long enough that
// ordinary week-to-week work is not constantly hedged. It is a heuristic and it
// is deliberately conservative: the cost of an unnecessary caveat is a clause,
// and the cost of a missing one is an agent acting on a fortnight-old plan.
const staleAfterDays = 7

// shortDate renders a recorded-at date, or "" when there is none. Always with
// the year: a bare "27 Jul" is ambiguous on any vault old enough to matter, and
// the two characters saved are not worth the class of mistake they buy.
func shortDate(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2 Jan 2006")
}

func daysOld(ts int64) int {
	if ts == 0 {
		return 0
	}
	return int(time.Since(time.Unix(ts, 0)).Hours() / 24)
}

// humanAge spells the age out. project.Age renders "13d ago", which is right
// for a dense listing and wrong in a sentence warning someone that what they
// are reading has gone off.
func humanAge(ts int64) string {
	switch d := daysOld(ts); {
	case ts == 0:
		return ""
	case d <= 0:
		return "written today"
	case d == 1:
		return "1 day old"
	default:
		return fmt.Sprintf("%d days old", d)
	}
}

// flatten collapses whitespace without shortening. Use it wherever the content
// is something someone wrote on purpose.
func flatten(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

// oneLine flattens and clips. Use it for labels and summaries — titles,
// headings, list entries whose job is to be scannable — never for findings.
func oneLine(s string) string {
	s = flatten(s)
	if len(s) > 160 {
		s = s[:159] + "…"
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func clip(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
