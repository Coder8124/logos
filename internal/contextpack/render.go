package contextpack

import (
	"fmt"
	"strings"

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
	p.renderCheckpoint(&b, checkpoint)
	section(&b, "Recorded since, not yet checkpointed", keptWorking, "\n")
	section(&b, "The project", keptProj, "\n")
	section(&b, "From the vault", keptNotes, "\n\n")
	section(&b, "What you've told me", keptMems, "\n")
	section(&b, "Still open", keptLoops, "\n")

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
	var body strings.Builder
	if c.Task != "" {
		fmt.Fprintf(&body, "**They were doing:** %s\n\n", strings.TrimSpace(c.Task))
	}
	if c.State != "" {
		fmt.Fprintf(&body, "%s\n\n", strings.TrimSpace(c.State))
	}
	list(&body, "Decided", c.Decisions)
	// The most valuable lines in the pack: what has already been ruled out.
	list(&body, "Already tried, didn't work", c.Failed)
	list(&body, "Still open", c.Questions)
	list(&body, "Files touched", c.Files)
	if c.Next != "" {
		fmt.Fprintf(&body, "**Next step:** %s\n", strings.TrimSpace(c.Next))
	}

	allowance := sp.allowance(secCheckpoint)
	text, trimmed := fit(body.String(), allowance)
	cost := estimate(text)
	sp.spent += cost
	sp.carry = max(0, allowance-cost)
	dropped := 0
	if trimmed {
		dropped = 1
		p.Excluded = append(p.Excluded, "part of the checkpoint body (budget)")
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
	fmt.Fprintf(b, "\n## Where we left off\n\nLast checkpoint by **%s**, %s.", who, project.Age(c.TS))
	if c.HandoffTo != "" {
		fmt.Fprintf(b, " Handed off to **%s**.", c.HandoffTo)
	}
	fmt.Fprintf(b, "\n\n%s\n", body)
}

func (p *Pack) wantWorking() []string {
	want := make([]string, 0, len(p.Working))
	for _, n := range p.Working {
		want = append(want, "- "+oneLine(n.Text))
	}
	return want
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
		head := fmt.Sprintf("### %s  `%s`", h.Title, h.Slug)
		if h.Via != "" {
			head += fmt.Sprintf("\n_reached through the graph from %s — not a direct match_", h.Via)
		}
		want = append(want, head+"\n\n"+demote(strings.TrimSpace(h.Body)))
		slugs = append(slugs, h.Slug)
	}
	return want, slugs
}

// wantMemories renders what the assistant has been told, with the provenance
// that decides how much to trust it. A preference stated by hand and one
// inferred from a passing remark should not read identically.
func (p *Pack) wantMemories() []string {
	var want []string
	seen := map[int64]bool{}
	for _, m := range p.Preferences {
		want = append(want, fmt.Sprintf("- preference: %s  _(%s)_", oneLine(m.Text), m.Source))
		seen[m.ID] = true
	}
	for _, m := range p.Related {
		if seen[m.ID] {
			continue
		}
		want = append(want, fmt.Sprintf("- %s: %s  _(%s, confidence %.2f)_",
			m.Kind, oneLine(m.Text), m.Source, m.Confidence))
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

// demote pushes headings inside a quoted note body below the pack's own heading
// levels. A daily note's "## Observations" rendered verbatim sits at the same
// level as "## Still open" and reads as a section of the pack rather than as a
// piece of the note being quoted.
func demote(body string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "#") {
			lines[i] = "#" + l
		}
	}
	return strings.Join(lines, "\n")
}

func list(b *strings.Builder, heading string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "**%s:**\n", heading)
	for _, it := range items {
		fmt.Fprintf(b, "- %s\n", strings.TrimSpace(it))
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

func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:159] + "…"
	}
	return s
}

func clip(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
