package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/insight"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/scope"
	"github.com/Coder8124/logos/internal/spend"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// dash is everything the usage dashboard draws, gathered before anything is
// drawn so the layout is a pure function of it and can be tested at any width.
type dash struct {
	who, window string
	days        []string // "2006-01-02", oldest first
	now         time.Time

	saved      usagepkg.Totals
	savedDaily []float64
	rate       float64
	recording  bool
	bad        int

	spend    *spend.Report
	spendErr error

	used, dormant []memory.Memory
	dormantN      int
	memNote       string

	recall     *recallFact
	recallNote string
}

// recallFact is the oldest dead end handed back in the window: how long it sat
// in the vault before it was needed, which is the thing a memory exists for.
type recallFact struct {
	text, agent  string
	ruled, shown int64
}

// usageDashboard draws `logos stats --usage` at a terminal. Piped, the command
// prints the text receipts instead; those carry each number's method in full,
// and a script reading them must not get box-drawing characters.
func usageDashboard(v, project string, week bool, rate float64) error {
	n := 30
	if week {
		n = 7
	}
	d, err := gatherDash(v, project, n, rate, time.Now())
	if err != nil {
		return err
	}
	w := 100
	if cols, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && cols > 0 {
		w = cols
	}
	fmt.Print(renderDash(d, w))
	return nil
}

// gatherDash fails only when the usage ledger cannot be read, as the piped
// receipts do: drawn anyway, every Logos panel would read "nothing recorded"
// for a ledger that may hold months. A host's transcripts or the index
// failing is shown in its own panel, since the rest of the page still holds.
func gatherDash(v, project string, n int, rate float64, now time.Time) (dash, error) {
	d := dash{who: "every project", window: fmt.Sprintf("the last %d days", n), now: now, rate: rate, recording: usagepkg.Recording(v)}
	if project != "" {
		d.who = project
	}
	start := time.Date(now.Year(), now.Month(), now.Day()-(n-1), 0, 0, 0, 0, now.Location())
	for i := 0; i < n; i++ {
		d.days = append(d.days, start.AddDate(0, 0, i).Format("2006-01-02"))
	}

	events, bad, err := usagepkg.Read(v)
	if err != nil {
		return d, fmt.Errorf("reading the usage ledger: %w", err)
	}
	d.bad = bad
	events = usagepkg.Within(events, start.Unix())
	d.saved = usagepkg.Sum(events, project)
	daily := map[string]float64{}
	for _, e := range events {
		if e.Kind == usagepkg.KindPack && (project == "" || usagepkg.SameProject(e.Project, project)) {
			daily[time.Unix(e.TS, 0).Format("2006-01-02")] += float64(e.Full - e.Sent)
		}
	}
	for _, day := range d.days {
		d.savedDaily = append(d.savedDaily, daily[day])
	}

	d.spend, d.spendErr = spend.Read(start, project)

	if _, err := os.Stat(filepath.Join(v, ".logos", "index.db")); err != nil {
		d.memNote = "no index yet — `logos index` builds it"
		d.recallNote = d.memNote
		return d, nil
	}
	ix, err := index.Open(v)
	if err != nil {
		d.memNote = "opening the index: " + err.Error()
		d.recallNote = d.memNote
		return d, nil
	}
	defer ix.Close()
	gatherMemories(&d, ix, project)
	gatherRecall(&d, v, ix, project, events)
	return d, nil
}

func gatherMemories(d *dash, ix *index.Index, project string) {
	var mems []memory.Memory
	var err error
	if project == "" {
		mems, err = memory.All(ix.DB)
	} else {
		mems, err = memory.AllInProject(ix.DB, scope.NormalizeArg(project))
	}
	if err != nil {
		d.memNote = "reading memories: " + err.Error()
		return
	}
	for _, m := range mems {
		if m.Uses > 0 {
			d.used = append(d.used, m)
		}
		if insight.Dormant(m, d.now) {
			d.dormant = append(d.dormant, m)
		}
	}
	sort.SliceStable(d.used, func(i, j int) bool { return d.used[i].Uses > d.used[j].Uses })
	sort.SliceStable(d.dormant, func(i, j int) bool { return insight.LastTouched(d.dormant[i]) < insight.LastTouched(d.dormant[j]) })
	d.dormantN = len(d.dormant)
	if len(mems) == 0 {
		d.memNote = "no memories yet"
	}
}

// gatherRecall finds, among the dead ends handed back in the window, the one
// recorded longest before it was handed back. The ledger names rulings by a
// fingerprint only, so they are matched against the vault's checkpoints; one
// since retired or deleted is simply not found.
func gatherRecall(d *dash, v string, ix *index.Index, project string, events []usagepkg.Event) {
	shown := map[string]int64{} // ref -> first time shown in the window
	for _, e := range events {
		if e.Kind != usagepkg.KindDeadEnd && e.Kind != usagepkg.KindAtEdit {
			continue
		}
		if project != "" && !usagepkg.SameProject(e.Project, project) {
			continue
		}
		for _, r := range e.Refs {
			if _, ok := shown[r]; !ok {
				shown[r] = e.TS
			}
		}
	}
	if len(shown) == 0 {
		d.recallNote = "no dead end was handed back in " + d.window
		return
	}
	rulings, err := deadend.Collect(v, ix.DB, "")
	if err != nil {
		d.recallNote = "reading checkpoints: " + err.Error()
		return
	}
	for _, r := range rulings {
		at, ok := shown[usagepkg.Ref(r.Record.Raw)]
		if !ok || r.When == 0 || at < r.When {
			continue
		}
		if d.recall == nil || at-r.When > d.recall.shown-d.recall.ruled {
			d.recall = &recallFact{text: r.Text, agent: r.Agent, ruled: r.When, shown: at}
		}
	}
	if d.recall == nil {
		d.recallNote = fmt.Sprintf("%d dead end%s handed back; none still in the vault's checkpoints", len(shown), pluralS(len(shown)))
	}
}

// renderDash lays the panels out two to a row when the terminal is wide
// enough to read them side by side, one to a row when it is not.
func renderDash(d dash, width int) string {
	width = min(max(width, 40), 140)
	var b strings.Builder
	fmt.Fprintf(&b, "logos stats --usage — %s, %s\n", d.who, d.window)
	if !d.recording {
		b.WriteString("· recording is off — Logos's own totals stopped when it was turned off. `logos stats --usage on` resumes it.\n")
	}
	if d.bad > 0 {
		fmt.Fprintf(&b, "· %d unreadable ledger line%s skipped — the totals are missing them\n", d.bad, pluralS(d.bad))
	}
	b.WriteString("\n")

	type panel struct {
		title string
		body  func(w int) []string
	}
	panels := []panel{
		{"Tokens saved", d.savedPanel},
		{"Agent spend", d.spendPanel},
		{"Memories", d.memoryPanel},
		{"Longest recall", d.recallPanel},
		{"Session size", d.sessionPanel},
	}
	if width < 100 {
		for _, p := range panels {
			b.WriteString(strings.Join(box(p.title, p.body(width-4), width), "\n"))
			b.WriteString("\n")
		}
	} else {
		colW := (width - 1) / 2
		for i := 0; i < len(panels); i += 2 {
			left := box(panels[i].title, panels[i].body(colW-4), colW)
			var right []string
			if i+1 < len(panels) {
				right = box(panels[i+1].title, panels[i+1].body(colW-4), colW)
			}
			for j := 0; j < max(len(left), len(right)); j++ {
				l, r := blankBox(left, j, colW), blankBox(right, j, 0)
				b.WriteString(strings.TrimRight(l+" "+r, " ") + "\n")
			}
		}
	}
	// The receipts hold what has no panel here — accepted memories, avoided
	// retries, procedures — and each number's method, so say how to reach them.
	b.WriteString("\nThe full receipts, with each number's method: `logos stats --usage | cat`\n")
	return b.String()
}

func blankBox(lines []string, j, w int) string {
	if j < len(lines) {
		return lines[j]
	}
	return strings.Repeat(" ", w)
}

// box frames body in a rounded border w columns wide. A line too long for it
// is wrapped, not cut: each box is drawn whole before two are set side by
// side, so a taller one only leaves blank rows beside its neighbour.
func box(title string, body []string, w int) []string {
	in := w - 4
	top := "╭─ " + title + " " + strings.Repeat("─", max(w-5-utf8.RuneCountInString(title), 0)) + "╮"
	out := []string{top}
	for _, l := range body {
		lines := []string{l}
		if utf8.RuneCountInString(l) > in {
			lines = wrap(l, in)
		}
		for _, wl := range lines {
			out = append(out, "│ "+fit(wl, in)+" │")
		}
	}
	return append(out, "╰"+strings.Repeat("─", w-2)+"╯")
}

func fit(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:max(n-1, 0)]) + "…"
	}
	return s + strings.Repeat(" ", n-len(r))
}

// spark draws vs as block heights, scaled to the largest. An empty day is
// a dot, not the lowest bar, so a quiet day never reads as a small one.
func spark(vs []float64, n int) string {
	if len(vs) > n {
		vs = vs[len(vs)-n:]
	}
	top := 0.0
	for _, v := range vs {
		top = max(top, v)
	}
	bars := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range vs {
		if v <= 0 || top == 0 {
			b.WriteRune('·')
			continue
		}
		b.WriteRune(bars[min(int(v/top*float64(len(bars)-1)+0.5), len(bars)-1)])
	}
	return b.String()
}

// human writes a token count the way the eye reads it: 1.2M, 412K, 830.
func human(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dK", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func dollars(f float64) string {
	if f >= 1000 {
		return "$" + thousands(int(f+0.5))
	}
	return fmt.Sprintf("$%.2f", f)
}

func ago(from, to int64) string {
	days := (to - from) / 86400
	switch {
	case days >= 60:
		return fmt.Sprintf("%d months", days/30)
	case days >= 1:
		return fmt.Sprintf("%d day%s", days, pluralS(int(days)))
	}
	h := (to - from) / 3600
	return fmt.Sprintf("%d hour%s", h, pluralS(int(h)))
}

func (d dash) savedPanel(w int) []string {
	t := d.saved
	if t.Packs == 0 && t.DeadEndChecks == 0 && t.AtEdit == 0 && t.Restores == 0 && t.Handoffs == 0 && t.Avoided == 0 && t.Procedures == 0 {
		return []string{"nothing recorded in " + d.window, "", "fills as context packs are sent (resume, context)", "and dead ends are handed back (before_you_try, tried, why)"}
	}
	out := []string{
		fmt.Sprintf("~%s tokens left out of %d pack%s", human(int64(t.Saved())), t.Packs, pluralS(t.Packs)),
		fmt.Sprintf("sent ~%s of ~%s drawn from", human(int64(t.Sent)), human(int64(t.Full))),
	}
	if d.rate > 0 {
		out = append(out, fmt.Sprintf("≈ %s at $%g per million, the rate you gave", dollars(float64(t.Saved())*d.rate/1e6), d.rate))
	}
	out = append(out, "")
	out = append(out, daily(d.savedDaily, d.days, w)...)
	out = append(out, "",
		fmt.Sprintf("%d dead end%s handed back before a retry; %d restore%s, %d handoff%s",
			t.Rulings, pluralS(t.Rulings), t.Restores, pluralS(t.Restores), t.Handoffs, pluralS(t.Handoffs)),
		"each pack against its own candidates — not your API bill")
	return out
}

// daily draws one bar per day with the first and last day named under it.
// A week in a wide box gets up to three columns a day, so its bars read as a
// chart and its two dates fit under them.
func daily(vs []float64, days []string, w int) []string {
	if len(vs) > w {
		vs, days = vs[len(vs)-w:], days[len(days)-w:]
	}
	if len(vs) == 0 {
		return nil
	}
	per := min(max(w/len(vs), 1), 3)
	var bars strings.Builder
	for _, r := range spark(vs, len(vs)) {
		bars.WriteString(strings.Repeat(string(r), per))
	}
	label := func(s string) string {
		t, _ := time.Parse("2006-01-02", s)
		return t.Format("2 Jan")
	}
	a, z := label(days[0]), label(days[len(days)-1])
	axis := a
	if gap := len(vs)*per - utf8.RuneCountInString(a) - utf8.RuneCountInString(z); gap >= 1 {
		axis = a + strings.Repeat(" ", gap) + z
	}
	return []string{bars.String(), axis}
}

func (d dash) spendPanel(w int) []string {
	if d.spendErr != nil {
		return []string{"could not read the hosts' transcripts:", d.spendErr.Error()}
	}
	r := d.spend
	if r == nil || len(r.Hosts) == 0 {
		return []string{"no Claude Code or Codex transcripts found on this machine"}
	}
	if r.Total.Total() == 0 {
		return []string{fmt.Sprintf("no agent requests in %s (%s)", d.window, strings.Join(r.Hosts, ", "))}
	}
	out := []string{fmt.Sprintf("%s across %s tokens", dollars(r.Total.USD), human(r.Total.Total()))}
	if r.Total.Unpriced > 0 {
		out = append(out, fmt.Sprintf("+ %s tokens from models with no list price", human(r.Total.Unpriced)))
	}
	var perDay []float64
	for _, day := range d.days {
		if b := r.Days[day]; b != nil {
			perDay = append(perDay, b.USD+float64(b.Unpriced)*1e-12) // an unpriced day still shows a bar
		} else {
			perDay = append(perDay, 0)
		}
	}
	out = append(out, "")
	out = append(out, daily(perDay, d.days, w)...)
	out = append(out, "", "Top models")
	out = append(out, spendRows(r.Models, w)...)
	out = append(out, "", "Top projects")
	out = append(out, spendRows(r.Projects, w)...)
	out = append(out, "", "Tokens the hosts recorded ("+strings.Join(r.Hosts, ", ")+"), priced at")
	out = append(out, wrap(spend.PriceSource, w)...)
	if r.Unreadable > 0 {
		out = append(out, fmt.Sprintf("%d unreadable transcript line%s skipped", r.Unreadable, pluralS(r.Unreadable)))
	}
	if r.Skipped > 0 {
		out = append(out, fmt.Sprintf("%d transcript%s could not be read to the end; the totals are missing the rest", r.Skipped, pluralS(r.Skipped)))
	}
	return out
}

func spendRows(m map[string]*spend.Bucket, w int) []string {
	var out []string
	for _, k := range spend.Top(m, 5) {
		b := m[k]
		// A "+" marks a row whose dollars leave some of its tokens out — a
		// project that ran on an unpriced model too — so it is not read as
		// the whole cost.
		price := dollars(b.USD)
		switch {
		case b.Unpriced == b.Total():
			price = "no list price"
		case b.Unpriced > 0:
			price += "+"
		}
		right := fmt.Sprintf("%13s %8s", price, human(b.Total()))
		n := max(w-2-utf8.RuneCountInString(right)-1, 4)
		// Model and project names come from the hosts' transcripts, which a
		// repository's folder name can reach.
		out = append(out, "  "+fit(terminalSafe(k, n), n)+" "+right)
	}
	return out
}

func (d dash) memoryPanel(w int) []string {
	if d.memNote != "" {
		return []string{d.memNote}
	}
	out := []string{"Most used"}
	if len(d.used) == 0 {
		out = append(out, "  none drawn on yet")
	}
	for _, m := range d.used[:min(len(d.used), 5)] {
		n := fmt.Sprintf("×%d", m.Uses)
		out = append(out, fmt.Sprintf("  %-5s %s", n, terminalSafe(m.Text, w-9)))
	}
	out = append(out, "", fmt.Sprintf("Taking space — %d unused for %d+ days", d.dormantN, int(insight.DormantAfter.Hours()/24)))
	for _, m := range d.dormant[:min(len(d.dormant), 5)] {
		age := fmt.Sprintf("%dd", (d.now.Unix()-insight.LastTouched(m))/86400)
		out = append(out, fmt.Sprintf("  %-5s %s", age, terminalSafe(m.Text, w-9)))
	}
	if d.dormantN > 0 {
		out = append(out, "", "`logos stats` lists them; `logos forget` or pin_memory decides")
	}
	return out
}

func (d dash) recallPanel(w int) []string {
	if d.recall == nil {
		return []string{d.recallNote}
	}
	r := d.recall
	who := ""
	if r.agent != "" {
		who = " by " + terminalSafe(r.agent, 40)
	}
	out := []string{
		ago(r.ruled, r.shown) + " between ruling it out and needing it",
		"",
	}
	// Four lines of the route: enough to recognise it, and `logos stats
	// --usage` piped or `why` has the rest.
	text := wrap(terminalSafe(r.text, 400), w)
	out = append(out, text[:min(len(text), 4)]...)
	out = append(out, "",
		fmt.Sprintf("ruled out%s on %s", who, time.Unix(r.ruled, 0).Format("2 Jan 2006")),
		"handed back on "+time.Unix(r.shown, 0).Format("2 Jan 2006"))
	return out
}

func (d dash) sessionPanel(w int) []string {
	if d.spend == nil || len(d.spend.Sessions) == 0 {
		return []string{"no agent sessions in " + d.window}
	}
	by := map[string][]float64{}
	for _, s := range d.spend.Sessions {
		by[s.Model] = append(by[s.Model], float64(s.Peak))
	}
	models := make([]string, 0, len(by))
	for m := range by {
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool {
		if len(by[models[i]]) != len(by[models[j]]) {
			return len(by[models[i]]) > len(by[models[j]])
		}
		return models[i] < models[j]
	})
	out := []string{"largest context per session, oldest to newest;", "then the median and how many sessions", ""}
	for _, m := range models[:min(len(models), 5)] {
		peaks := by[m]
		sorted := append([]float64(nil), peaks...)
		sort.Float64s(sorted)
		right := fmt.Sprintf("%6s · %d", human(int64(sorted[len(sorted)/2])), len(peaks))
		name := 18
		sw := max(w-name-utf8.RuneCountInString(right)-4, 4)
		out = append(out, fmt.Sprintf("  %s %s %s", fit(terminalSafe(m, name), name), fit(spark(peaks, sw), sw), right))
	}
	return out
}

// wrap breaks s into lines of at most w runes at spaces.
func wrap(s string, w int) []string {
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		if line != "" && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > w {
			out = append(out, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}
