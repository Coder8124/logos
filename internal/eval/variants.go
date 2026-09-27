package eval

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"strings"
)

// Variants.
//
// Thirty-two cases asked one way each cannot tell a real improvement from a
// lucky phrasing: one case flipping moves the pass rate by three points. The
// recall floor made the cost concrete. It broke handoff-superseded-plan only
// when the task was vague, and the suite caught it because that case happened
// to be worded vaguely — a sharper wording would have hidden it.
//
// So each case can also run with the question asked differently and with
// unrelated history around it. Both are still hand-written or drawn from a
// hand-written pool, for the reason the suite gives against generated cases:
// gold labels must stay auditable, and the labels here never change — only
// what surrounds them does.
//
// Each axis is varied on its own rather than crossed, so a failing variant
// names its cause: a case that fails reworded and passes with distractors is
// sensitive to wording, not to noise.

// Axes a variant can differ on. The empty axis is the case as written.
const (
	AxisWording     = "reworded"
	AxisDistractors = "distractors"
)

// Expand returns every scenario as written, followed by one case per extra
// wording and one per distractor seed. seeds <= 0 adds no distractor cases.
func Expand(suite []Scenario, seeds int) []Scenario {
	var out []Scenario
	for _, sc := range suite {
		sc.Base = sc.ID
		out = append(out, sc)
		for i, w := range sc.Wordings {
			v := sc
			v.ID = fmt.Sprintf("%s~w%d", sc.ID, i+1)
			v.Axis, v.Variant = AxisWording, fmt.Sprintf("%q", w)
			v.Query.Task = w
			out = append(out, v)
		}
		for seed := 1; seed <= seeds; seed++ {
			v := sc
			v.ID = fmt.Sprintf("%s~d%d", sc.ID, seed)
			v.Axis, v.Variant = AxisDistractors, fmt.Sprintf("distractor set %d", seed)
			v.Setup = append(append([]Event(nil), sc.Setup...), distractors(sc, seed)...)
			out = append(out, v)
		}
	}
	return out
}

// goldTerms is every string a gold label matches on. A wording or distractor
// that contains one could pass or fail a case by itself, and the variant would
// then be measuring the harness rather than the system.
func goldTerms(g Gold) []string {
	var out []string
	for _, fs := range [][]Fact{g.Carry, g.Avoid, g.Signal} {
		for _, f := range fs {
			out = append(out, f.Any...)
			out = append(out, f.All...)
		}
	}
	return out
}

func containsAny(text string, terms []string) bool {
	hay := normalize(text)
	for _, t := range terms {
		if strings.Contains(hay, normalize(t)) {
			return true
		}
	}
	return false
}

// distractorPool is ordinary traffic from the same programme the cases are set
// in. The filler topics are office admin an embedding separates from a BOM
// question easily; these share the case's world and vocabulary, which is what
// a real project's history around one checkpoint looks like.
var distractorPool = []string{
	"Marketing wants the launch video cut to ninety seconds.",
	"The charging case lid hinge squeaks on the EVT units; logged for mechanical.",
	"Colour team picked graphite and sand for the first two colourways.",
	"The companion app pairing flow needs a second pass from design.",
	"Customer support asked for a spare nose-pad SKU.",
	"The Bluetooth antenna passed pre-scan with 3dB of margin.",
	"Photography for the retail box is booked for the fourteenth.",
	"The speaker grille mesh supplier sent revised samples.",
	"Regulatory asked for updated battery label artwork.",
	"Firmware build 0.9.3 fixes the wake-word false triggers.",
	"The prescription insert partner wants an NDA before sharing lens data.",
	"EVT units shipped to the three pilot customers.",
	"The retail demo stand needs a locking USB-C port.",
	"Sales wants a volume forecast by region before the board meeting.",
	"Legal reviewed the privacy notice for the camera indicator LED.",
	"The design team is moving its files to the new shared drive.",
	"The touch strip on the right temple misreads wet fingers.",
	"Industrial design wants a matte finish on the temple tips.",
	"Packaging copy is waiting on the final product name.",
	"The Reno warehouse can hold two thousand units.",
	"Supplier audit of the frame vendor is scheduled for next quarter.",
	"The camera module passed its focus calibration on the pilot line.",
	"Pilot customers asked for a quieter charging chime.",
	"The accessibility review flagged low contrast on the setup screens.",
}

// distractors picks a seeded handful from the pool for one case. The seed is
// mixed with the case id so two cases do not get the same set, and the same
// case gets the same set on every run — a variant that changed between runs
// would reintroduce the noise it exists to measure. Anything that contains
// one of the case's gold terms is skipped.
func distractors(sc Scenario, seed int) []Event {
	h := fnv.New64a()
	h.Write([]byte(sc.ID))
	rng := rand.New(rand.NewSource(int64(h.Sum64()) + int64(seed)))

	terms := goldTerms(sc.Gold)
	const n = 10
	var out []Event
	for i, idx := range rng.Perm(len(distractorPool)) {
		if len(out) == n {
			break
		}
		text := distractorPool[idx]
		if containsAny(text, terms) {
			continue
		}
		days := 1 + rng.Intn(40)
		// Where the case is a project's history, the noise is that project's
		// working notes, from more than one agent; otherwise it is things the
		// user said, alongside the facts the case asks about.
		if sc.Query.Project != "" {
			actor := []string{"claude", "cursor"}[i%2]
			out = append(out, note(days, actor, sc.Query.Project, text))
		} else {
			out = append(out, said(days, text))
		}
	}
	return out
}

// hasVariants reports whether any score came from a variant.
func hasVariants(results []Result) bool {
	for _, r := range results {
		for _, s := range r.Scores {
			if s.Axis != "" {
				return true
			}
		}
	}
	return false
}

// asWritten keeps only the scores for scenarios as written. The headline stays
// on those so a number from a variant run can be set beside every earlier one.
func asWritten(results []Result) []Result {
	out := make([]Result, 0, len(results))
	for _, r := range results {
		kept := Result{Adapter: r.Adapter}
		for _, s := range r.Scores {
			if s.Axis == "" {
				kept.Scores = append(kept.Scores, s)
			}
		}
		out = append(out, kept)
	}
	return out
}

// stability reports how each system holds up away from the wording and
// history each case was written with, and names every case whose outcome
// depends on the variant — for the first-listed system, the one under test.
func stability(results []Result) string {
	var b strings.Builder
	b.WriteString("\n── across variants ───────────────────────────────────────────────────────\n\n")
	b.WriteString(fmt.Sprintf("%-18s %11s %10s %12s %11s\n", "system", "as written", AxisWording, AxisDistractors, "consistent"))
	for _, r := range results {
		pass := map[string]float64{}
		n := map[string]float64{}
		outcomes := map[string]map[bool]bool{}
		var bases []string
		for _, s := range r.Scores {
			n[s.Axis]++
			if s.Pass() {
				pass[s.Axis]++
			}
			if outcomes[s.Base] == nil {
				outcomes[s.Base] = map[bool]bool{}
				bases = append(bases, s.Base)
			}
			outcomes[s.Base][s.Pass()] = true
		}
		consistent := 0
		for _, base := range bases {
			if len(outcomes[base]) == 1 {
				consistent++
			}
		}
		rate := func(axis string) string {
			if n[axis] == 0 {
				return "—"
			}
			return fmt.Sprintf("%.1f%%", pass[axis]/n[axis]*100)
		}
		b.WriteString(fmt.Sprintf("%-18s %11s %10s %12s %11s\n", r.Adapter,
			rate(""), rate(AxisWording), rate(AxisDistractors), fmt.Sprintf("%d/%d", consistent, len(bases))))
	}
	b.WriteString("\nconsistent = the case passed on every variant, or failed on every one.\n")

	if len(results) == 0 {
		return b.String()
	}
	subject := results[0]
	type group struct {
		pass, n int
		failed  []string
	}
	groups := map[string]*group{}
	var order []string
	for _, s := range subject.Scores {
		g := groups[s.Base]
		if g == nil {
			g = &group{}
			groups[s.Base] = g
			order = append(order, s.Base)
		}
		g.n++
		if s.Pass() {
			g.pass++
			continue
		}
		label := "as written"
		if s.Axis != "" {
			label = s.Axis + " " + s.Variant
		}
		g.failed = append(g.failed, label)
	}
	var mixed []string
	for _, base := range order {
		g := groups[base]
		if g.pass > 0 && g.pass < g.n {
			mixed = append(mixed, fmt.Sprintf("  %-34s %d/%d  failed: %s", base, g.pass, g.n, strings.Join(g.failed, "; ")))
		}
	}
	if len(mixed) == 0 {
		b.WriteString(fmt.Sprintf("\nevery case gave %s the same outcome on every variant.\n", subject.Adapter))
		return b.String()
	}
	b.WriteString(fmt.Sprintf("\ncases whose outcome depends on the variant (%s):\n", subject.Adapter))
	b.WriteString(strings.Join(mixed, "\n") + "\n")
	return b.String()
}
