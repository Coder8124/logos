package procedure

import (
	"fmt"
	"strings"

	"github.com/Coder8124/brain/internal/project"
	"github.com/Coder8124/brain/internal/untrusted"
)

// Render writes the "known to work" half of before_you_try.
//
// Unlike deadend.Render, silence here is not a finding worth stating —
// "nothing recorded" needs no caveat the way "nothing rules this out" does on
// the dead-end side, because absence of a procedure was never going to read
// as approval or disapproval of anything. So an empty result renders nothing
// at all, and the caller omits the section entirely rather than showing it
// empty.
//
// Every payload field goes through untrusted.Inline before it lands here —
// a route or a trap is vault content a prior agent wrote, and this output
// reaches a model exactly like a dead-end ruling does.
func Render(hits []Hit) string {
	if len(hits) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## What is known to work\n\n%s already bear%s on this.\n\n",
		count(len(hits)), plural(len(hits), "s", ""))

	for _, h := range hits {
		who := h.Agent
		if who == "" {
			who = "someone"
		}
		fmt.Fprintf(&b, "- **%s**", untrusted.Inline(h.Record.Route))
		if h.Record.Trap != "" {
			fmt.Fprintf(&b, " — otherwise, %s", untrusted.Inline(h.Record.Trap))
		}
		fmt.Fprintf(&b, " — recorded by %s, %s", untrusted.Inline(who), project.Age(h.When))
		if h.Elsewhere {
			fmt.Fprintf(&b, ", on **%s** rather than the project you are working on", untrusted.Inline(h.Project))
		}
		b.WriteString("\n")
		if tags := recordTags(h.Record); tags != "" {
			fmt.Fprintf(&b, "  %s\n", tags)
		}
		if h.Record.Verify != "" {
			fmt.Fprintf(&b, "  verify: %s\n", untrusted.Inline(h.Record.Verify))
		}
		if h.Stale {
			b.WriteString("  ⚠ possibly superseded — version-bound and old enough that the dependency it names may have moved since\n")
		}
	}

	if anyElsewhere(hits) {
		b.WriteString("\n_Procedures marked as from another project may not transfer. Check the constraint that made it necessary still applies here._\n")
	}
	return b.String()
}

func recordTags(r Record) string {
	var parts []string
	if r.Layer != "" {
		parts = append(parts, string(r.Layer))
	}
	if r.Scope != "" {
		parts = append(parts, string(r.Scope))
	}
	if r.Evidence != "" {
		parts = append(parts, string(r.Evidence))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

func anyElsewhere(hits []Hit) bool {
	for _, h := range hits {
		if h.Elsewhere {
			return true
		}
	}
	return false
}

func count(n int) string {
	switch n {
	case 1:
		return "One recorded procedure"
	default:
		return fmt.Sprintf("%d recorded procedures", n)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
