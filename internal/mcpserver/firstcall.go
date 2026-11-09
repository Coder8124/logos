package mcpserver

import (
	"fmt"

	"github.com/Coder8124/logos/internal/setup"
)

// startHooked reports whether this host's session already opened with the
// handoff, injected by a session-start hook. Every answer is read from disk
// rather than assumed from the host's name: Claude Code registered with
// `claude mcp add` has no plugin and so no hook, and Cursor or Codex only have
// one if setup wrote it.
var startHooked = func(agent string) bool {
	switch agent {
	case "claude-code":
		return setup.LogosPluginRecord().Connects
	case "cursor":
		return setup.StartHookInstalled("Cursor")
	case "codex":
		return setup.StartHookInstalled("Codex")
	}
	return false
}

// handsOverItself lists the tools whose own answer is the handoff, so a first
// call to one of them needs nothing added.
var handsOverItself = map[string]bool{"resume": true, "context": true}

// closesWithoutIt lists the tools that end a stretch of work. A model whose
// first call is one of them has already done the work the handoff was for, and
// after it the "last checkpoint" a resume would show is the one it just wrote,
// handed back as if a previous agent had left it.
var closesWithoutIt = map[string]bool{"checkpoint": true, "handoff": true}

// handoffDue decides, before the tool runs, which project's handoff this call
// should carry, or "" for none. Before, not after: a call that creates the
// project (a first note_progress in a new one) would otherwise find it existing
// and hand the model its own new work as where an earlier agent left off.
//
// Whatever is settled here is settled for the session, so a hooked host or a
// session with nothing to hand over pays for the check once, not per call.
func (s *Session) handoffDue(tool string) string {
	if s.started || handsOverItself[tool] || closesWithoutIt[tool] {
		return ""
	}
	if startHooked(s.clientAgent) {
		s.started = true
		return ""
	}
	// Only the project this session is in, and only one the vault holds.
	// Resume's fallback for no project is the most recent one in the vault, and
	// for an unknown name a "nothing filed under it" report: the first is a
	// guess a model that asked for nothing has no reason to doubt, the second
	// is noise on a call that was about something else.
	project := s.sessionProject()
	if project == "" || !s.projectExists(project) {
		s.started = true
		return ""
	}
	return project
}

// firstCallResume is the handoff a host with no session-start hook never got,
// carried on the first Logos tool result of the session that succeeded, or ""
// when there is nothing to add. project is what handoffDue decided before the
// call ran.
//
// In a host with a hook the session opens with where the last agent stopped and
// what it ruled out. In one without, that depended on the model choosing to call
// resume, which is the step it most often skips — and the one whose absence
// costs the most, because the next thing it does is re-try a dead end. Any
// first call is the earliest point the server can reach the model at all, so
// the handoff rides on that.
//
// After the tool's own answer, not before it: the model called for that answer,
// and a handoff above it pushes it out of the place it is read.
func (s *Session) firstCallResume(tool, project string) string {
	if s.started {
		return ""
	}
	if handsOverItself[tool] {
		s.startOffered = true
		return ""
	}
	if closesWithoutIt[tool] {
		s.started = true
		return ""
	}
	if project == "" {
		return ""
	}
	out, err := s.resume(project, "", 0, "")
	s.startOffered = true
	if err != nil {
		retry := ""
		if s.toolSet == nil || s.toolSet["resume"] {
			retry = " Call resume to try again."
		}
		return fmt.Sprintf("\n\n(Logos could not load where work on %s left off: %v.%s)", project, err, retry)
	}
	return fmt.Sprintf("\n\n---\n_This host has no session-start hook, so this is the first chance to hand over: where work on %s left off. Read it before you plan; you don't need to call resume._\n\n", project) + out
}

// startSent records whether the result carrying the handoff reached the host.
// A cancelled call's reply is dropped unread, so the handoff stays owed.
func (s *Session) startSent(delivered bool) {
	if s.startOffered && delivered {
		s.started = true
	}
	s.startOffered = false
}
