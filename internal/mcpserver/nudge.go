package mcpserver

import (
	"fmt"

	"github.com/Coder8124/logos/internal/session"
)

// notesBeforeNudge is how much unsaved progress it takes to be worth a line.
// One note is already answered by note_progress's own receipt — "uncommitted
// until you checkpoint" — and a warning that repeats what the previous line
// said teaches the model that neither means anything.
const notesBeforeNudge = 3

// unsavedNudge is the sentence a tool result carries when a project has
// recorded progress in this session and not been checkpointed, or "" when there
// is nothing to say.
//
// It is the floor under the transcript path in unsaved.go. That path needs a
// transcript reader for the host; Claude Desktop and Copilot have none, and
// Cursor's store can be locked while Cursor is running. This needs nothing: the
// model reads every tool result it gets, in every host there is, which no other
// channel we have is true of — stderr goes to a log nobody opens, and MCP
// receipts are not shown to the user at all in most hosts.
//
// Said once per project per stretch of unsaved work, not once per call. A
// sentence appended to every result is noise the model learns to skip past, and
// it would push the answer the tool was called for further from where it is
// read.
//
// Counted per project because notes and checkpoints are both per project: a
// session-wide count would let a checkpoint on one project silence unsaved work
// on another, and would let one note each on three projects read as a session
// with three notes to save.
func (s *Session) unsavedNudge() string {
	project, notes := "", 0
	for p, n := range s.notes {
		if n < notesBeforeNudge || s.nudgedFor[p] {
			continue
		}
		// The most unsaved project first, by name where two are level, so the
		// line does not depend on map iteration order.
		if n > notes || (n == notes && p < project) {
			project, notes = p, n
		}
	}
	if project == "" {
		return ""
	}
	// Not marked as said here. The response this is going into may never be
	// sent: a host that cancels a slow call has its reply dropped unread, and
	// spending the session's one warning on a result nobody saw is the exact
	// failure this exists to prevent. See nudgeSent.
	s.offered = project
	return fmt.Sprintf("\n\n(%d notes recorded on %s in this session and no checkpoint yet. "+
		"They stay uncommitted until you call checkpoint, and a session that ends without one leaves the next agent a list of files instead of what you ruled out.)", notes, project)
}

// nudgeSent records whether the result carrying the last nudge reached the
// host. A dropped one leaves it armed for the next call.
func (s *Session) nudgeSent(delivered bool) {
	if s.offered != "" && delivered {
		if s.nudgedFor == nil {
			s.nudgedFor = map[string]bool{}
		}
		s.nudgedFor[s.offered] = true
	}
	s.offered = ""
}

// notedProgress counts a note on project towards its nudge.
//
// Keyed by the scope a checkpoint will be filed under rather than by what the
// agent typed. Commit lowercases and dash-collapses the name, so counting the
// raw spelling meant a checkpoint on FleetBuilder cleared nothing and two
// spellings of one project were two projects, each with too few notes to
// mention.
func (s *Session) notedProgress(project string) {
	project = session.SafeScope(project)
	if project == "" {
		return
	}
	if s.notes == nil {
		s.notes = map[string]int{}
	}
	s.notes[project]++
}

// checkpointed clears what the nudge is about for one project. The count
// restarts rather than the nudge being spent for good: a long session that
// checkpoints in the middle and keeps working has as much unsaved again as it
// had the first time.
func (s *Session) checkpointed(project string) {
	project = session.SafeScope(project)
	delete(s.notes, project)
	delete(s.nudgedFor, project)
	if s.offered == project {
		s.offered = ""
	}
}
