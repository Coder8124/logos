package eval

import (
	"fmt"
	"sort"
	"strings"
)

// A Result is one adapter's full run.
type Result struct {
	Adapter string
	Scores  []Score
}

// Report renders the comparison.
//
// Fidelity leads because it is the number that cannot be gamed by returning
// more: it rises only when the required facts arrive and the wrong ones stay
// out. Density sits beside it for the opposite reason — a system can buy recall
// with tokens, and the column shows the price.
func Report(results []Result, verbose bool) string {
	var b strings.Builder

	b.WriteString("\n── overall ───────────────────────────────────────────────────────────────\n\n")
	b.WriteString(fmt.Sprintf("%-18s %7s %8s %8s %8s %8s %8s %8s\n",
		"system", "pass", "fidelity", "recall", "leak", "signal", "tokens", "dens/1k"))
	for _, r := range results {
		a := Roll(r.Scores, func(Score) string { return "all" })
		if len(a) == 0 {
			continue
		}
		o := a[0]
		b.WriteString(fmt.Sprintf("%-18s %6.1f%% %7.1f%% %7.1f%% %7.1f%% %7.1f%% %8d %8.1f\n",
			r.Adapter, o.PassRate*100, o.Fidelity*100, o.Recall*100, o.Leakage*100, o.Signal*100, o.Tokens, o.Density))
	}
	b.WriteString("\npass = met every bar the scenario set, including its signal labels.\n")
	b.WriteString("fidelity = recall × (1 − leakage). leak and signal are averaged only over\n")
	b.WriteString("the cases that test them. tokens is the mean response size; dens/1k is\n")
	b.WriteString("required facts carried per 1000 tokens — the cost of that recall.\n")

	// ---- by family -------------------------------------------------------
	families := distinct(results, func(s Score) string { return s.Family })
	b.WriteString("\n── pass rate by family ───────────────────────────────────────────────────\n\n")
	b.WriteString(fmt.Sprintf("%-18s", "system"))
	for _, f := range families {
		b.WriteString(fmt.Sprintf(" %14s", f))
	}
	b.WriteString("\n")
	for _, r := range results {
		byFam := map[string]Aggregate{}
		for _, a := range Roll(r.Scores, func(s Score) string { return s.Family }) {
			byFam[a.Group] = a
		}
		b.WriteString(fmt.Sprintf("%-18s", r.Adapter))
		for _, f := range families {
			if a, ok := byFam[f]; ok {
				b.WriteString(fmt.Sprintf(" %13.0f%%", a.PassRate*100))
			} else {
				b.WriteString(fmt.Sprintf(" %14s", "—"))
			}
		}
		b.WriteString("\n")
	}

	// ---- by skill --------------------------------------------------------
	b.WriteString("\n── pass rate by skill ────────────────────────────────────────────────────\n\n")
	skills := distinct(results, func(s Score) string { return s.Skill })
	b.WriteString(fmt.Sprintf("%-22s", "skill"))
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %11s", clip(r.Adapter, 11)))
	}
	b.WriteString("\n")
	for _, sk := range skills {
		b.WriteString(fmt.Sprintf("%-22s", clip(sk, 22)))
		for _, r := range results {
			var hit, n float64
			for _, s := range r.Scores {
				if s.Skill == sk {
					if s.Pass() {
						hit++
					}
					n++
				}
			}
			if n == 0 {
				b.WriteString(fmt.Sprintf(" %11s", "—"))
				continue
			}
			b.WriteString(fmt.Sprintf(" %10.0f%%", hit/n*100))
		}
		b.WriteString("\n")
	}

	// ---- the predictions we got wrong ------------------------------------
	b.WriteString(surprises(results))

	if verbose {
		b.WriteString(detail(results))
	}
	return b.String()
}

// surprises compares each scenario's recorded expectation against what the
// first-listed system actually did.
//
// This is the part of the report that keeps the suite honest over time. A case
// labelled a weakness that starts passing is progress; a case labelled a
// strength that starts failing is a regression the averages would otherwise
// absorb. Either way the label is now wrong and somebody has to look.
func surprises(results []Result) string {
	if len(results) == 0 {
		return ""
	}
	subject := results[0]

	var better, worse []string
	for _, s := range subject.Scores {
		switch {
		case s.Known == KnownWeakness && s.Pass():
			better = append(better, fmt.Sprintf("  %-34s marked a weakness, %s", s.Scenario, why(s)))
		case s.Known == KnownStrength && !s.Pass():
			worse = append(worse, fmt.Sprintf("  %-34s marked a strength, %s", s.Scenario, why(s)))
		}
	}
	if len(better) == 0 && len(worse) == 0 {
		return "\n── predictions ───────────────────────────────────────────────────────────\n\n" +
			fmt.Sprintf("  every case in the suite behaved as %s's labels predicted.\n", subject.Adapter)
	}

	var b strings.Builder
	b.WriteString("\n── predictions that were wrong ───────────────────────────────────────────\n\n")
	if len(worse) > 0 {
		b.WriteString("expected to pass, did not:\n")
		b.WriteString(strings.Join(worse, "\n") + "\n")
	}
	if len(better) > 0 {
		if len(worse) > 0 {
			b.WriteString("\n")
		}
		b.WriteString("expected to fail, passed:\n")
		b.WriteString(strings.Join(better, "\n") + "\n")
	}
	return b.String()
}

// why says which bar a scenario cleared or missed, so the line is actionable
// without reopening the suite.
func why(s Score) string {
	parts := []string{fmt.Sprintf("fidelity %.0f%%", s.Fidelity()*100)}
	if s.SignalTotal > 0 {
		parts = append(parts, fmt.Sprintf("signal %.0f%%", s.Signal()*100))
	}
	if s.Err != nil {
		parts = append(parts, "error")
	}
	return strings.Join(parts, ", ")
}

func detail(results []Result) string {
	var b strings.Builder
	b.WriteString("\n── per scenario ──────────────────────────────────────────────────────────\n")
	for _, r := range results {
		b.WriteString("\n" + r.Adapter + "\n")
		for _, s := range r.Scores {
			flag := " "
			if s.Over() {
				flag = "!"
			}
			b.WriteString(fmt.Sprintf("  %s %-34s fid %3.0f%%  %d/%d carried  %5d tok\n",
				flag, s.Scenario, s.Fidelity()*100, s.CarryHit, s.CarryTotal, s.Tokens))
			if len(s.Missed) > 0 {
				b.WriteString("      missed:  " + strings.Join(s.Missed, "; ") + "\n")
			}
			if len(s.Leaked) > 0 {
				b.WriteString("      leaked:  " + strings.Join(s.Leaked, "; ") + "\n")
			}
			if len(s.Unsignalled) > 0 {
				b.WriteString("      no signal: " + strings.Join(s.Unsignalled, "; ") + "\n")
			}
			if s.Err != nil {
				b.WriteString("      error:   " + s.Err.Error() + "\n")
			}
		}
	}
	return b.String()
}

func distinct(results []Result, key func(Score) string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range results {
		for _, s := range r.Scores {
			if k := key(s); !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// Composition summarises what the suite contains, so a reader can see the
// balance of strengths to weaknesses before reading any score.
func Composition(suite []Scenario) string {
	byFamily := map[string]int{}
	byKnown := map[Known]int{}
	for _, s := range suite {
		byFamily[s.Family]++
		byKnown[s.Known]++
	}
	fams := make([]string, 0, len(byFamily))
	for f := range byFamily {
		fams = append(fams, f)
	}
	sort.Strings(fams)

	var parts []string
	for _, f := range fams {
		parts = append(parts, fmt.Sprintf("%s %d", f, byFamily[f]))
	}
	return fmt.Sprintf("%d scenarios (%s) · %d expected strengths, %d known weaknesses",
		len(suite), strings.Join(parts, ", "), byKnown[KnownStrength], byKnown[KnownWeakness])
}
