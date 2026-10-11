package ops

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/Coder8124/logos/internal/memory"
)

// ReviewQueue is what every read reports while the review queue is not empty.
// Quarantine keeps an agent's memories out of recall until the user says yes,
// and a user who is never told there is anything to say yes to leaves them
// there for good — while the agent reads the empty recall as the fact never
// having been stored. A failed count is said, not swallowed, but does not fail
// the read it is attached to. shell is the command the user types, which the
// MCP server learns from its install.
func ReviewQueue(db *sql.DB, shell string) Outcome {
	var o Outcome
	// Adopt hand edits to the queue file first, as `logos review` does: a
	// proposal whose line the user deleted is one they rejected, and counting
	// it asks them to review it again. Said, because the read just discarded
	// something.
	restored, rejected, err := memory.ReconcilePending(db)
	if err != nil {
		o.fail(fmt.Sprintf("could not read the user's edits to the review queue: %v", err))
	} else {
		o.say(QueueAdoption(restored, rejected))
	}
	n, err := memory.PendingCount(db)
	switch {
	case err != nil:
		o.fail(fmt.Sprintf("could not count the memories waiting for review: %v", err))
	case n == 1:
		o.say(fmt.Sprintf("1 memory is waiting for your review — `%s review`", shell))
	case n > 1:
		o.say(fmt.Sprintf("%d memories are waiting for your review — `%s review`", n, shell))
	}
	return o
}

// QueueAdoption says what a hand edit to the review queue file did to the
// queue, in the words `logos review` prints for the same adoption. Empty when
// nothing changed, which is nearly always.
func QueueAdoption(restored, rejected int) string {
	var parts []string
	if rejected > 0 {
		parts = append(parts, fmt.Sprintf("%d %s rejected", rejected, proposals(rejected)))
	}
	if restored > 0 {
		parts = append(parts, fmt.Sprintf("%d %s taken from the file", restored, proposals(restored)))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("adopted the user's edit to %s/%s: %s", memory.Dir, memory.PendingFile, strings.Join(parts, ", "))
}

func proposals(n int) string {
	if n == 1 {
		return "proposal"
	}
	return "proposals"
}
