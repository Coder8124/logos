package mcpserver

import "fmt"

// notesBeforeNudge is how much unsaved progress it takes to be worth a line.
// One note is already answered by note_progress's own receipt — "uncommitted
// until you checkpoint" — and a warning that repeats what the previous line
// said teaches the model that neither means anything.
const notesBeforeNudge = 3

// unsavedNudge is the sentence a tool result carries when this session has
// recorded progress and never checkpointed, or "" when it has nothing to say.
//
// It is the floor under the transcript path in unsaved.go. That path needs a
// transcript reader for the host; Claude Desktop and Copilot have none, and
// Cursor's store can be locked while Cursor is running. This needs nothing: the
// model reads every tool result it gets, in every host there is, which no other
// channel we have is true of — stderr goes to a log nobody opens, and MCP
// receipts are not shown to the user at all in most hosts.
//
// Said once per stretch of unsaved work, not once per call. A sentence appended
// to every result is noise the model learns to skip past, and it would push the
// answer the tool was called for further from where it is read.
func (s *Session) unsavedNudge() string {
	// The count alone is the signal, because checkpointed resets it. Reading
	// lastCheckpoint here instead would suppress the nudge for the rest of a
	// session that checkpointed once and then worked for another hour.
	if s.nudged || s.notes < notesBeforeNudge {
		return ""
	}
	s.nudged = true
	return fmt.Sprintf("\n\n(%d notes recorded in this session and no checkpoint yet. "+
		"They stay uncommitted until you call checkpoint, and a session that ends without one leaves the next agent a list of files instead of what you ruled out.)", s.notes)
}

// notedProgress counts a note towards the nudge.
func (s *Session) notedProgress() { s.notes++ }

// checkpointed clears what the nudge is about. The count restarts rather than
// the nudge being spent for good: a long session that checkpoints in the middle
// and keeps working has as much unsaved again as it had the first time.
func (s *Session) checkpointed() {
	s.notes, s.nudged = 0, false
}
