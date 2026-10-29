package ops

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/textmatch"
	"github.com/Coder8124/logos/internal/untrusted"
	"github.com/Coder8124/logos/internal/vault"
)

// Ledger is what one WriteLedger pass wrote.
type Ledger struct {
	Path        string // vault-relative, slash-separated
	Checkpoints int    // how many it was derived from
	InForce     int    // decisions no later one reversed
	Superseded  int    // decisions a later one reversed, struck through
	RuledOut    int    // dead ends still standing
	MayNotHold  int    // of those, the ones whose named files changed since
	Retired     int    // dead ends a person retired with `logos review --stale`
	Questions   int    // open questions
	Worktrees   int    // linked worktree scopes read alongside the project's own
	// Measured is whether the dead ends were measured against a repository.
	// Without one "may no longer hold" was not asked, which is not the same
	// answer as "none changed".
	Measured bool
}

// WriteLedger writes a project's long-term history, derived from every
// checkpoint it has, to sessions/<project>/ledger.md, measuring each dead end
// against the repository at repoDir ("" for none).
//
// Checkpoints are short-term memory: resume reads a handful, and a decision
// or a dead end nobody restated falls out of every pack within a few dozen
// sessions however true it still is. The ledger ages by field instead of by
// position — a decision stays until a later one reverses it, a dead end stays
// until the files it names change, and then it is flagged rather than dropped,
// because a change to the file is evidence the cause moved, not proof.
//
// A view, never a record. Nothing here edits or removes a checkpoint, no
// table backs it, and it is rebuilt whole every time from markdown alone (the
// checkpoints, and reviewed.md for the rulings a person retired), so deleting
// it loses nothing. No model either: everything in it is a checkpoint's own
// structured field, quoted and cited, and what it adds around them says what
// was recorded and when — history, not a rulebook.
func WriteLedger(vaultDir, project, repoDir string) (Ledger, error) {
	scope := session.SafeScope(project)
	if scope == "" {
		return Ledger{}, fmt.Errorf("project name %q has no letters or digits to find its checkpoints by", project)
	}
	// The project's own checkpoints and those of every linked worktree under
	// it ("p/wt", as session.Scopes lists them and deadend.Collect reads
	// them): an agent working in a worktree checkpoints there, and a ledger of
	// the main folder alone left out every decision and dead end it recorded.
	names := []string{scope}
	all, err := session.Scopes(vaultDir)
	if err != nil {
		return Ledger{}, fmt.Errorf("listing %s's worktree checkpoints: %w", scope, err)
	}
	for _, s := range all {
		if strings.HasPrefix(s, scope+"/") {
			names = append(names, s)
		}
	}
	var scopes []scoped
	total := 0
	for _, s := range names {
		hist, err := session.History(vaultDir, s, 0)
		if err != nil {
			return Ledger{}, fmt.Errorf("reading %s's checkpoints: %w", s, err)
		}
		if len(hist) == 0 {
			continue
		}
		reviews, err := session.Reviews(vaultDir, s)
		if err != nil {
			// Not skipped: without it a ruling a person retired would come
			// back as standing, which is the review undone without saying so.
			return Ledger{}, fmt.Errorf("reading %s's review verdicts: %w", s, err)
		}
		scopes = append(scopes, scoped{name: s, hist: hist, reviews: reviews})
		total += len(hist)
	}
	if total == 0 {
		// Not an empty ledger: a file saying "nothing was decided" about a
		// project whose name was mistyped is a wrong answer, not no answer.
		return Ledger{}, fmt.Errorf("no checkpoints for %q, so there is no history to derive a ledger from", scope)
	}
	text, l := renderLedger(scope, scopes, gitstate.NewAnchors(repoDir))
	l.Path = path.Join(session.CheckpointDir, scope, session.LedgerFile)
	// No lock: the content is a function of the checkpoints, and the write is
	// an atomic rename, so two passes racing leave one whole ledger either way.
	if err := vault.WriteAtomic(filepath.Join(vaultDir, filepath.FromSlash(l.Path)), []byte(text)); err != nil {
		return Ledger{}, fmt.Errorf("writing %s: %w", l.Path, err)
	}
	return l, nil
}

// LedgerReceipt is the line `logos ledger` prints for what it wrote.
func LedgerReceipt(l Ledger) string {
	measured := fmt.Sprintf("%d may no longer hold", l.MayNotHold)
	if !l.Measured {
		measured = "not measured against the code: not run from the project's repository"
	}
	retired := ""
	if l.Retired > 0 {
		retired = fmt.Sprintf(", %d retired in review", l.Retired)
	}
	wts := ""
	if l.Worktrees > 0 {
		wts = fmt.Sprintf(" (read with %d linked %s)", l.Worktrees, plural(l.Worktrees, "worktree", "worktrees"))
	}
	return fmt.Sprintf("ledger: %d %s in force, %d superseded, %d ruled out (%s)%s, %d open %s — %s%s",
		l.InForce, plural(l.InForce, "decision", "decisions"), l.Superseded,
		l.RuledOut, measured, retired,
		l.Questions, plural(l.Questions, "question", "questions"), l.Path, wts)
}

// ruling is one dead end across every checkpoint that recorded it.
type ruling struct {
	text    string   // the newest wording
	slugs   []string // newest first
	retired bool     // every copy retired in review
	drift   *gitstate.Drift
}

// asked is one open question across every checkpoint that asked it.
type asked struct {
	text  string
	slugs []string
}

// scoped is one checkpoint folder the ledger reads: the project's own, or a
// linked worktree's under it, with that folder's own review verdicts.
type scoped struct {
	name    string
	hist    []session.Checkpoint // newest first
	reviews map[string]session.Review
}

// renderLedger is the ledger's text, a function of its arguments only — no
// clock, no map order — so the same checkpoints rebuild it byte for byte.
// scopes is the project's own folder first, when it has checkpoints, then its
// worktrees in name order.
func renderLedger(scope string, scopes []scoped, a *gitstate.Anchors) (string, Ledger) {
	// One history for the dead ends and questions, newest first across every
	// folder, each checkpoint ruled on by its own folder's review verdicts.
	var hist []session.Checkpoint
	reviewsOf := map[string]map[string]session.Review{}
	for _, sc := range scopes {
		hist = append(hist, sc.hist...)
		for _, c := range sc.hist {
			reviewsOf[c.Slug] = sc.reviews
		}
	}
	if len(scopes) > 1 {
		sort.SliceStable(hist, func(i, j int) bool {
			if hist[i].TS != hist[j].TS {
				return hist[i].TS > hist[j].TS
			}
			return hist[i].Slug > hist[j].Slug
		})
	}
	l := Ledger{Checkpoints: len(hist), Measured: a != nil}
	var wts []string
	for _, sc := range scopes {
		if sc.name != scope {
			wts = append(wts, strings.TrimPrefix(sc.name, scope+"/"))
		}
	}
	l.Worktrees = len(wts)
	cite := citer(scope)
	var b strings.Builder

	// The span reads as a range, oldest to newest, so two dates side by side
	// are not taken for the only two checkpoints read.
	span := cite([]string{hist[0].Slug})
	if len(hist) > 1 {
		span = cite([]string{hist[len(hist)-1].Slug}) + " to " + span
	}
	fmt.Fprintf(&b, "---\ntype: ledger\nproject: %s\n---\n\n# Ledger: %s\n\n", scope, scope)
	from := ""
	if len(wts) > 0 {
		from = fmt.Sprintf(" (with linked %s %s)", plural(len(wts), "worktree", "worktrees"), quote(strings.Join(wts, ", ")))
	}
	fmt.Fprintf(&b, "_Derived by `logos ledger %s` from %d %s%s, %s. The checkpoints are the record: "+
		"this file is rebuilt from them whole, and deleting it loses nothing. Each entry quotes the checkpoints it cites._\n\n",
		scope, len(hist), plural(len(hist), "checkpoint", "checkpoints"), from, span)
	b.WriteString(untrusted.Boundary + "\n\n")

	// Decisions: grouped and matched exactly as PromoteDecisions groups and
	// supersedes them, over the same newest DecisionWindow checkpoints it
	// reads. The whole history gives a decision different first and last
	// times, and so a different verdict: a replacement once said long before
	// the window reads as a neighbour here and a reversal there, and the ledger
	// struck what memory still held. Only a decision promote cannot see, one
	// with no checkpoint in the window, is judged on the whole history.
	//
	// Per folder, as promotion runs per folder: a worktree's decisions are
	// judged among its own checkpoints, so the ledger agrees with what each
	// pass promoted, and one already listed from an earlier folder is not
	// listed twice.
	var standing, struck []string
	listed := map[string]bool{}
	for _, sc := range scopes {
		s, x := decisionLines(sc.hist, cite, listed)
		standing, struck = append(standing, s...), append(struck, x...)
	}
	l.InForce, l.Superseded = len(standing), len(struck)
	b.WriteString("## Decisions in force\n\n")
	b.WriteString("_Each as last worded. Struck through: a later checkpoint took the same question up differently " +
		"after the struck answer was last recorded — not one carried beside it in a parallel conversation._\n\n")
	section(&b, append(standing, struck...))

	rulings, ruleOrder := rulingsIn(hist, reviewsOf, a)
	var live, retired []string
	for _, k := range ruleOrder {
		r := rulings[k]
		if r.retired {
			retired = append(retired, fmt.Sprintf("- ~~%s~~ — retired with `logos review --stale` — %s\n", deadEnd(r.text), cite(r.slugs)))
			continue
		}
		line := fmt.Sprintf("- %s — %s\n", deadEnd(r.text), cite(r.slugs))
		if r.drift != nil {
			line += "  - ⚠ " + quote(r.drift.Note()) + "\n"
			l.MayNotHold++
		}
		live = append(live, line)
	}
	l.RuledOut, l.Retired = len(live), len(retired)
	b.WriteString("## Ruled out\n\n")
	if a != nil {
		b.WriteString("_Each stands until the files it names change; then it is flagged, not dropped._\n\n")
	} else {
		b.WriteString("_Not measured against the code: the ledger was not built from the project's repository._\n\n")
	}
	section(&b, append(live, retired...))

	questions, qOrder := questionsIn(hist)
	var open []string
	for _, k := range qOrder {
		q := questions[k]
		open = append(open, fmt.Sprintf("- %s — %s\n", quote(q.text), cite(q.slugs)))
	}
	l.Questions = len(open)
	b.WriteString("## Open questions\n\n")
	b.WriteString("_As asked. A later checkpoint that settled one without saying so leaves it listed._\n\n")
	section(&b, open)

	return strings.TrimRight(b.String(), "\n") + "\n", l
}

// decisionLines is one folder's decisions as ledger lines, standing and
// struck, skipping and then marking those already listed.
func decisionLines(hist []session.Checkpoint, cite func([]string) string, listed map[string]bool) (standing, struck []string) {
	_, order, byKey := heldIn(hist)
	winOrder, winKey, older := inWindow(hist, order, byKey)
	winBy, oldBy := supersessions(winOrder, winKey), supersessions(older, byKey)
	for _, k := range order {
		if listed[k] {
			continue
		}
		listed[k] = true
		h, by := byKey[k], winBy[k]
		if winKey[k] == nil {
			by = oldBy[k]
		}
		if by == nil {
			standing = append(standing, fmt.Sprintf("- %s — %s\n", quote(h.text), cite(h.slugs)))
			continue
		}
		// The replacement is cited where it took the question up again, as
		// judged where it was judged — in the window or on the whole history.
		r := byKey[by.key]
		took := by.slugs[takenUp(byKeyFor(winKey, k, byKey), by)]
		struck = append(struck, fmt.Sprintf("- ~~%s~~ — superseded by %s (%s) — recorded in %s\n",
			quote(h.text), quote(r.text), cite([]string{took}), cite(h.slugs)))
	}
	return standing, struck
}

// byKeyFor is k's held as its supersession was judged: in the window when it
// has a checkpoint there, on the whole history otherwise.
func byKeyFor(winKey map[string]*held, k string, byKey map[string]*held) *held {
	if h := winKey[k]; h != nil {
		return h
	}
	return byKey[k]
}

// inWindow cuts the whole history's helds down to the newest DecisionWindow
// checkpoints, the ones promotion reads: winKey and winOrder are what heldIn
// over that window would give, and older lists the keys with no checkpoint in
// it. Derived from the one full pass rather than a second heldIn over the
// window, which read and keyed every windowed checkpoint's decisions twice.
func inWindow(hist []session.Checkpoint, order []string, byKey map[string]*held) (winOrder []string, winKey map[string]*held, older []string) {
	if len(hist) <= session.DecisionWindow {
		return order, byKey, nil
	}
	win := make(map[string]bool, session.DecisionWindow)
	for _, c := range hist[:session.DecisionWindow] {
		win[c.Slug] = true
	}
	winKey = map[string]*held{}
	for _, k := range order {
		h := byKey[k]
		n := 0 // slugs are newest first, so the window's are a prefix
		for n < len(h.slugs) && win[h.slugs[n]] {
			n++
		}
		if n == 0 {
			older = append(older, k)
			continue
		}
		w := *h
		w.slugs, w.when, w.conv = h.slugs[:n], h.when[:n], h.conv[:n]
		w.in = make(map[string]bool, n)
		for _, sl := range w.slugs {
			w.in[sl] = true
		}
		w.first = w.when[n-1]
		winKey[k] = &w
		winOrder = append(winOrder, k)
	}
	return winOrder, winKey, older
}

func section(b *strings.Builder, lines []string) {
	if len(lines) == 0 {
		b.WriteString("_None recorded._\n\n")
		return
	}
	for _, line := range lines {
		b.WriteString(line)
	}
	b.WriteString("\n")
}

// rulingsIn is every dead end in the history, keyed as resume and `logos
// review --stale` key them, in order of their newest appearance.
//
// Measured the way resume measures one (contextpack's markRulings): from the
// newest copy a person has not retired, at the commit a re-confirm moved it
// to, structured anchor included. So the ledger flags exactly what --stale
// would offer to settle, and settling it there clears it here.
func rulingsIn(hist []session.Checkpoint, reviewsOf map[string]map[string]session.Review, a *gitstate.Anchors) (map[string]*ruling, []string) {
	out := map[string]*ruling{}
	var order []string
	measured := map[string]bool{}
	for _, c := range hist { // newest first
		for _, f := range c.Failed {
			if session.IsPlaceholder(f) {
				continue
			}
			k := session.ReviewKey(f)
			r := out[k]
			if r == nil {
				r = &ruling{text: f, retired: true}
				out[k] = r
				order = append(order, k)
			}
			if n := len(r.slugs); n > 0 && r.slugs[n-1] == c.Slug {
				continue // said twice in one checkpoint is still one source
			}
			r.slugs = append(r.slugs, c.Slug)
			gone, commit := reviewsOf[c.Slug][k].For(c.TS, c.Git.Commit)
			if gone {
				continue
			}
			r.retired = false
			if measured[k] {
				continue
			}
			measured[k] = true
			rec := deadend.ParseRecord(textmatch.Flatten(f))
			if d, ok := a.SinceAnchor(commit, rec.Anchor, rec.Raw); ok {
				r.drift = &d
			}
		}
	}
	return out, order
}

func questionsIn(hist []session.Checkpoint) (map[string]*asked, []string) {
	out := map[string]*asked{}
	var order []string
	for _, c := range hist {
		for _, q := range c.Questions {
			if session.IsPlaceholder(q) {
				continue
			}
			k := session.ReviewKey(q)
			if out[k] == nil {
				out[k] = &asked{text: q}
				order = append(order, k)
			}
			if n := len(out[k].slugs); n == 0 || out[k].slugs[n-1] != c.Slug {
				out[k].slugs = append(out[k].slugs, c.Slug)
			}
		}
	}
	return out, order
}

// deadEnd renders a dead end as resume renders one: a typed record as its
// route, what happened, its tags and what to try instead; prose as it was.
func deadEnd(f string) string {
	r := deadend.ParseRecord(f)
	if !r.Typed() {
		return quote(f)
	}
	s := quote(r.Route)
	if r.Observation != "" {
		s += " — " + quote(r.Observation)
	}
	if tags := deadend.Tags(r); tags != "" {
		s += " · " + quote(tags)
	}
	if r.Alternative != "" {
		s += " — try instead: " + quote(r.Alternative)
	}
	return s
}

// citer links checkpoints from the ledger, which sits in the project's own
// folder, by the path below it. Newest first in; written oldest to newest,
// and a long run is its two ends and a count — a ruling restated in ninety
// checkpoints is cited, not listed ninety times.
func citer(scope string) func(slugs []string) string {
	prefix := session.CheckpointDir + "/" + scope + "/"
	link := func(slug string) string {
		return fmt.Sprintf("[%s](%s.md)", path.Base(slug), strings.TrimPrefix(slug, prefix))
	}
	return func(slugs []string) string {
		switch n := len(slugs); n {
		case 0:
			return ""
		case 1:
			return link(slugs[0])
		case 2:
			return link(slugs[1]) + ", " + link(slugs[0])
		default:
			return fmt.Sprintf("%s … %s (%d checkpoints)", link(slugs[n-1]), link(slugs[0]), n)
		}
	}
}

// quote is checkpoint text made safe to sit in a ledger line. Inline keeps it
// to the one line, so it cannot open a section of its own; the escapes keep it
// from forging what the ledger's own lines are made of — a strikethrough that
// marks a standing decision reversed, a link that passes for a citation, a
// code span that hides the citation after it, or markup a renderer would act
// on — while every word of it still reads. A leading list marker is escaped
// too: every quote follows a ledger "- ", and "- ⚠ ..." after it renders as a
// nested bullet, a drift flag the ledger never raised.
func quote(s string) string {
	s = ledgerEscapes.Replace(untrusted.Inline(s))
	if s == "" {
		return s
	}
	if strings.ContainsRune("#>-*+", rune(s[0])) {
		return `\` + s
	}
	if d := len(s) - len(strings.TrimLeft(s, "0123456789")); d > 0 && d < len(s) && (s[d] == '.' || s[d] == ')') {
		return s[:d] + `\` + s[d:]
	}
	return s
}

var ledgerEscapes = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`, "~", `\~`, "<", `\<`, "`", "\\`")
