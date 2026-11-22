package session

import (
	"fmt"

	"github.com/Coder8124/logos/internal/text"
	"github.com/Coder8124/logos/internal/untrusted"
)

// TurnSpan is the stretch of the host transcript a checkpoint's lines came
// from: the turns after the previous checkpoint of the same conversation, up
// to the count this one recorded. Numbered from one, inclusive.
//
// Worked out when read rather than stored beside the count. The start is
// another checkpoint's count, so a stored start would go stale the moment an
// earlier checkpoint was refiled or removed, and the frontmatter would carry
// two numbers that could disagree. ok is false for a checkpoint that recorded
// no count — then no span is claimed, because a guessed one would send the
// reader to turns that never said the line.
//
// others is the checkpoints to look for a predecessor among; c itself, and
// any that are not of the same conversation, are ignored. A predecessor that
// is missing from others (an older checkpoint the caller did not load) makes
// the span start earlier than it should, never later: it can over-include a
// turn, but cannot cut out the one the line came from.
func TurnSpan(c Checkpoint, others []Checkpoint) (from, to int, ok bool) {
	if c.Turns <= 0 {
		return 0, 0, false
	}
	start := 0
	for _, o := range others {
		if o.Slug == c.Slug || !sameConversation(c, o) {
			continue
		}
		if o.Turns > start && o.Turns < c.Turns {
			start = o.Turns
		}
	}
	return start + 1, c.Turns, true
}

// sameConversation reports whether two checkpoints were written in one host
// conversation. An auto record built before the hook named its session has no
// host_session, and is matched on the transcript its state names instead.
func sameConversation(a, b Checkpoint) bool {
	if a.HostSession != "" || b.HostSession != "" {
		return a.HostSession == b.HostSession
	}
	return a.Auto && b.Auto && a.State != "" && a.State == b.State
}

// SourcePointer is the line printed after a ruled-out line to say where in the
// transcript it came from, or "" when that is not known: no recorded count, or
// no host session to name. Always rendered from frontmatter at read time and
// never written into a checkpoint or a note — the dead-end collector re-reads
// both, and the pointer's words would come back as a new ruling.
func SourcePointer(c Checkpoint, others []Checkpoint) string {
	if c.HostSession == "" || c.Slug == "" {
		return ""
	}
	from, to, ok := TurnSpan(c, others)
	if !ok {
		return ""
	}
	span := fmt.Sprintf("turn %d", to)
	if from < to {
		span = fmt.Sprintf("turns %d-%d", from, to)
	}
	return fmt.Sprintf("source: %s of session %s, `logos transcript %s`",
		span, text.Truncate(untrusted.Inline(c.HostSession), 8), untrusted.Inline(c.Slug))
}
