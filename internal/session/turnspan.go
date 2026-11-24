package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coder8124/logos/internal/vault"

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
		// Only an agent's own checkpoint is a predecessor. An auto record's
		// count is how far a sweep read, not where an agent stopped
		// writing, and starting after it would skip turns the lines came
		// from.
		if o.Auto || o.Slug == c.Slug || !sameConversation(c, o) {
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

// ConversationCheckpoints is every checkpoint filed in the folders of one host
// conversation, read from those folders alone. A span needs one predecessor,
// and loading the project's whole history to find it made every receipt, every
// transcript read and every resume pay for a year of checkpoints. A
// checkpoint from before conversation folders existed is not here, which can
// only make a span start earlier (see TurnSpan), never later.
func ConversationCheckpoints(vaultDir, hostSession string) []Checkpoint {
	folder := dropSymbols(hostSession)
	if folder == "" {
		return nil
	}
	var out []Checkpoint
	for _, dir := range convoDirsFor(vaultDir, folder) {
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !IsCheckpointFile(f.Name()) || vault.IsConflictCopy(dir, f.Name()) {
				continue
			}
			full := filepath.Join(dir, f.Name())
			raw, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			c := ParseCheckpoint(string(raw))
			if c.Empty() {
				continue
			}
			rel, err := filepath.Rel(vaultDir, full)
			if err != nil {
				continue
			}
			c.Slug = strings.TrimSuffix(filepath.ToSlash(rel), ".md")
			out = append(out, c)
		}
	}
	return out
}

// Predecessors is what TurnSpan should look through for c: its conversation's
// folder when it names a host session, else its project's history (an auto
// record built before the hook named a session is matched on its state).
func Predecessors(vaultDir string, c Checkpoint) []Checkpoint {
	if c.HostSession != "" {
		return ConversationCheckpoints(vaultDir, c.HostSession)
	}
	all, _ := History(vaultDir, c.Project, 0)
	return all
}
