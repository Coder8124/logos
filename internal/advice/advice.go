// Package advice is what a checkpoint receipt says about the checkpoint it just
// wrote: the placeholders it dropped and the fields it kept as given but doubts.
//
// The CLI and the MCP server used to build these sentences each for itself,
// and they drifted — a nudge added to one front end reached agents on that
// host only, and a wording fix had to be found and made twice. The checks and
// the sentences live here; a front end supplies only how its fields are
// spelled, "--next" on a command line and `next` in a tool call.
package advice

import (
	"fmt"

	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/session"
)

// Fields is how one front end names a checkpoint's fields, so a nudge tells
// the agent the exact thing to type. The paired nouns are singular and plural.
type Fields struct {
	Next, Questions, Intent, Failed, Verified string
	Decision, RuledOut                        [2]string
}

// CLI is `logos checkpoint`'s spelling.
var CLI = Fields{
	Next: "--next", Questions: "--question", Intent: "--intent", Failed: "--failed", Verified: "--verified",
	Decision: [2]string{"--decided entry", "--decided entries"},
	RuledOut: [2]string{"--failed entry", "--failed entries"},
}

// MCP is the checkpoint tool's spelling.
var MCP = Fields{
	Next: "`next`", Questions: "`questions`", Intent: "`intent`", Failed: "`failed`", Verified: "`verified`",
	Decision: [2]string{"decision", "decisions"},
	RuledOut: [2]string{"ruled-out approach", "ruled-out approaches"},
}

// Checkpoint returns the receipt's sentences for c, in the order an agent
// should act on them. dropped is how many placeholder failed entries the
// caller removed before committing; earlier is the project's history as it
// stood before this checkpoint, which the intent check reads.
//
// Every sentence after the first is "recorded as given": a checkpoint is
// written when context is running out, so nothing here refuses one — it says
// the doubt out loud instead.
func Checkpoint(c session.Checkpoint, earlier []session.Checkpoint, dropped int, f Fields) []string {
	var out []string
	if dropped > 0 {
		// Said out loud so the agent knows its "none" was not kept as a dead end.
		out = append(out, fmt.Sprintf("Dropped %s from %s; leave it empty when nothing was ruled out.",
			count(dropped, [2]string{"placeholder entry", "placeholder entries"}), f.Failed))
	}
	if session.ClaimsDoneUnverified(c) {
		out = append(out, fmt.Sprintf("Recorded as given; the state says the work is done but %s is empty, so the next agent takes that on trust — add the command that showed it.", f.Verified))
	}
	if session.NextReadsAsMoreThanOneStep(c.Next) {
		out = append(out, fmt.Sprintf("Recorded as given; %s reads as more than one step — the parts that are conditional or later usually belong in %s, which resume prints as \"Still open\".", f.Next, f.Questions))
	}
	if n := session.DecisionsWithoutReason(c.Decisions); n > 0 {
		out = append(out, fmt.Sprintf("Recorded as given; %s without a reason — \"X, because Y\" lets the next agent see what forced it without rereading the transcript.", count(n, f.Decision)))
	}
	if session.IntentDropped(c, earlier) {
		out = append(out, fmt.Sprintf("Recorded as given; no intent carried: this task's wording matches no earlier checkpoint that gave its reason, though the work before it had one, so resume will say what is being done but not why — pass %s again when a task is reworded.", f.Intent))
	}
	if n := deadend.UnplacedToolchain(c.Failed); n > 0 {
		out = append(out, fmt.Sprintf("Recorded as given; %s about a tool, package manager or PATH with no layer — one that is about this machine's toolchain rather than the code belongs as `route: ... | observation: ... | layer: environment`, so an agent on another toolchain can tell it does not apply to them.", count(n, f.RuledOut)))
	}
	return out
}

func count(n int, noun [2]string) string {
	if n == 1 {
		return "1 " + noun[0]
	}
	return fmt.Sprintf("%d %s", n, noun[1])
}
