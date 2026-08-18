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

// Render writes the pack as markdown for a model to read, spending the budget
// section by section. It fills in Budget and Excluded as a side effect.
func (p *Pack) Render() string {
	sp := newSpender(p.Budget.Limit)
	p.Excluded = nil
	p.Sources = nil

	var b strings.Builder
	if p.Task != "" {
		fmt.Fprintf(&b, "# Context for: %s\n", oneLine(p.Task))
	} else {
		fmt.Fprintf(&b, "# Context for %s\n", p.Hint)
	}
	switch {
	case p.Project != nil:
		fmt.Fprintf(&b, "\nProject **%s** — last active %s.\n",
			p.Project.Name, project.Age(p.Project.LastActive))
	case p.Checkpoint != nil:
		// No project note, but there is a record of work. Saying "no project
		// matched" here would tell the agent to disregard the very thing it is
		// about to be handed.
		fmt.Fprintf(&b, "\nProject **%s** — no note in the vault yet, but there is a work history below.\n",
			p.scope())
	default:
		hint := p.Hint
		if hint == "" {
			hint = p.Task
		}
		fmt.Fprintf(&b, "\n_No project matched %q — standing context only._\n", oneLine(hint))
	}

	p.renderCheckpoint(&b, sp)
	p.renderWorking(&b, sp)
	p.renderProject(&b, sp)
	p.renderNotes(&b, sp)
	p.renderMemories(&b, sp)
	p.renderLoops(&b, sp)
	p.renderSources(&b)

	p.Budget.Spent = sp.spent
	p.Budget.By = sp.lines
	p.renderBudget(&b)
	return b.String()
}

// renderCheckpoint prints where the last agent stopped. It leads the pack
// because it is the one section that cannot be reconstructed from anything
// else — everything below is retrievable, this is testimony.
func (p *Pack) renderCheckpoint(b *strings.Builder, sp *spender) {
	c := p.Checkpoint
	if c == nil {
		return
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

	text, trimmed := fit(body.String(), sp.allowance(secCheckpoint))
	sp.spent += estimate(text)
	sp.carry = max(0, sp.allowance(secCheckpoint)-estimate(text))
	dropped := 0
	if trimmed {
		dropped = 1
		p.Excluded = append(p.Excluded, "part of the checkpoint body (budget)")
	}
	sp.lines = append(sp.lines, Line{Section: secCheckpoint, Tokens: estimate(text), Items: 1, Dropped: dropped})

	who := c.Agent
	if who == "" {
		who = "an agent"
	}
	fmt.Fprintf(b, "\n## Where we left off\n\nLast checkpoint by **%s**, %s.", who, project.Age(c.TS))
	if c.HandoffTo != "" {
		fmt.Fprintf(b, " Handed off to **%s**.", c.HandoffTo)
	}
	fmt.Fprintf(b, "\n\n%s\n", text)
	if c.Slug != "" {
		p.Sources = append(p.Sources, c.Slug)
	}
}

// renderWorking prints progress recorded since the last checkpoint — the
// uncommitted half of continuity.
func (p *Pack) renderWorking(b *strings.Builder, sp *spender) {
	if len(p.Working) == 0 {
		return
	}
	want := make([]string, 0, len(p.Working))
	for _, n := range p.Working {
		want = append(want, "- "+oneLine(n.Text))
	}
	kept := sp.take(secWorking, want)
	if len(kept) == 0 {
		return
	}
	b.WriteString("\n## Recorded since, not yet checkpointed\n\n")
	b.WriteString(strings.Join(kept, "\n") + "\n")
	p.noteDropped(secWorking, len(want)-len(kept))
}

func (p *Pack) renderProject(b *strings.Builder, sp *spender) {
	pr := p.Project
	if pr == nil {
		return
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
	kept := sp.take(secProject, want)
	if len(kept) == 0 {
		return
	}
	b.WriteString("\n## The project\n\n")
	b.WriteString(strings.Join(kept, "\n") + "\n")
	p.noteDropped(secProject, len(want)-len(kept))
	p.Sources = append(p.Sources, pr.Slug)
}

// renderNotes prints vault prose. Every chunk carries its slug so the consumer
// can cite the note rather than restate it as if it were its own knowledge.
func (p *Pack) renderNotes(b *strings.Builder, sp *spender) {
	if len(p.Notes) == 0 {
		return
	}
	want := make([]string, 0, len(p.Notes))
	slugs := make([]string, 0, len(p.Notes))
	for _, h := range p.Notes {
		head := fmt.Sprintf("### %s  `%s`", h.Title, h.Slug)
		if h.Via != "" {
			head += fmt.Sprintf("\n_reached through the graph from %s — not a direct match_", h.Via)
		}
		want = append(want, head+"\n\n"+strings.TrimSpace(h.Body))
		slugs = append(slugs, h.Slug)
	}
	kept := sp.take(secNotes, want)
	if len(kept) == 0 {
		return
	}
	b.WriteString("\n## From the vault\n\n")
	b.WriteString(strings.Join(kept, "\n\n") + "\n")
	p.Sources = append(p.Sources, slugs[:len(kept)]...)
	for _, s := range slugs[len(kept):] {
		p.Excluded = append(p.Excluded, s+" (budget)")
	}
}

// renderMemories prints what the assistant has been told, with the provenance
// that decides how much to trust it. A preference stated by hand and one
// inferred from a passing remark should not read identically.
func (p *Pack) renderMemories(b *strings.Builder, sp *spender) {
	var want []string
	for _, m := range p.Preferences {
		want = append(want, fmt.Sprintf("- preference: %s  _(%s)_", oneLine(m.Text), m.Source))
	}
	seen := map[int64]bool{}
	for _, m := range p.Preferences {
		seen[m.ID] = true
	}
	for _, m := range p.Related {
		if seen[m.ID] {
			continue
		}
		want = append(want, fmt.Sprintf("- %s: %s  _(%s, confidence %.2f)_",
			m.Kind, oneLine(m.Text), m.Source, m.Confidence))
	}
	kept := sp.take(secMemories, want)
	if len(kept) == 0 {
		return
	}
	b.WriteString("\n## What you've told me\n\n")
	b.WriteString(strings.Join(kept, "\n") + "\n")
	p.noteDropped(secMemories, len(want)-len(kept))
}

func (p *Pack) renderLoops(b *strings.Builder, sp *spender) {
	if len(p.OpenLoops) == 0 {
		return
	}
	want := make([]string, 0, len(p.OpenLoops))
	for _, c := range p.OpenLoops {
		want = append(want, "- "+loopLine(c))
	}
	kept := sp.take(secLoops, want)
	if len(kept) == 0 {
		return
	}
	b.WriteString("\n## Still open\n\n")
	b.WriteString(strings.Join(kept, "\n") + "\n")
	p.noteDropped(secLoops, len(want)-len(kept))
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
