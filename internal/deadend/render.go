package deadend

import (
	"fmt"
	"strings"

	"github.com/Coder8124/brain/internal/project"
)

// Render writes the interruption.
//
// The wording is doing real work here, and it is not decoration. This output
// lands in front of a model that is about to propose something, and there are
// two ways to get it wrong. Too soft — "you may wish to consider" — and it gets
// skimmed past. Too hard — "this will not work" — and it stops an agent from
// retrying something that deserves a second look, which is worse, because a
// dead end recorded a year ago under different constraints is evidence, not a
// verdict.
//
// So the shape is: state plainly that it was tried, say by whom and when, and
// hand the decision back. The agent is told to say out loud that it knows,
// because the visible version of this — "we tried that in March, the drop test
// failed at 1.2m" — is the difference between a memory that stores and a
// memory that helps.
func Render(proposed string, hits []Ruling) string {
	if len(hits) == 0 {
		return fmt.Sprintf("No record of anyone trying %q. Nothing in the vault rules it out — "+
			"which is not the same as it being a good idea, only that it is not a repeat.\n",
			oneLine(proposed))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## This has been tried\n\n%s already bear%s on %q.\n\n",
		count(len(hits)), plural(len(hits), "s", ""), oneLine(proposed))

	for _, h := range hits {
		who := h.Agent
		if who == "" {
			who = "someone"
		}
		fmt.Fprintf(&b, "- **%s** — tried by %s, %s", h.Text, who, project.Age(h.When))
		if h.Elsewhere {
			fmt.Fprintf(&b, ", on **%s** rather than the project you are working on", h.Project)
		}
		if h.Source == FromNote {
			b.WriteString(", recorded in a working note that was never checkpointed")
		}
		if h.Slug != "" {
			fmt.Fprintf(&b, " · `%s`", h.Slug)
		}
		b.WriteString("\n")
	}

	b.WriteString("\nBefore proposing this, say that it has been tried and what happened. ")
	b.WriteString("If you still think it is right, say what is different now — ")
	b.WriteString("a dead end recorded under other constraints is evidence, not a verdict.\n")

	if anyElsewhere(hits) {
		b.WriteString("\n_Rulings marked as from another project may not transfer. Check the constraint that caused the failure still applies here._\n")
	}
	return b.String()
}

func anyElsewhere(hits []Ruling) bool {
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
		return "One recorded dead end"
	default:
		return fmt.Sprintf("%d recorded dead ends", n)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if len(s) > 120 {
		s = s[:119] + "…"
	}
	return s
}
