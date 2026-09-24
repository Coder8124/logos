package ingest

import (
	"fmt"
	"strings"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/text"
	"github.com/Coder8124/logos/internal/transcript"
)

// maxAutoCommands keeps an auto checkpoint readable after a long session; the
// latest commands are the ones nearest where it stopped. The hook-fed path in
// cmd/logos holds the same number for the same reason.
const maxAutoCommands = 20

// AutoCheckpoint records a session that ended without one, built from the
// host's own transcript.
//
// The hook-fed path builds the same record from the activity log, which only
// exists where the host runs hooks. A host without them — Cursor, Codex,
// Claude Desktop — never tells Logos anything about the edits and commands it
// ran, so the MCP server has nothing to reconstruct from and the session was
// simply lost. The transcript the host writes for itself has all of it, and
// reading that needs no cooperation from the host at all.
//
// Nothing here is a claim. Harvest lists only what mechanically ran; verified,
// failed and next stay empty, because the next agent treats a failed entry as a
// paid-for ruling and will not re-try what it names.
//
// Returns nil when the session did no work, or when this transcript has already
// been recorded.
func AutoCheckpoint(vaultDir string, s *transcript.Session, project string) (*session.Checkpoint, error) {
	return autoCheckpoint(vaultDir, s, project, 0)
}

// autoCheckpoint is AutoCheckpoint dated at, or now when at is zero. The sweep
// records sessions that ended hours ago, and dating those now would put a list
// of files above the reviewed handoff somebody wrote after them.
func autoCheckpoint(vaultDir string, s *transcript.Session, project string, at int64) (*session.Checkpoint, error) {
	if s == nil {
		return nil, nil
	}
	c, ok := autoRecord(s, project, at)
	if !ok {
		return nil, nil
	}
	// Provenance in the record itself, not only in a log: an unverified
	// checkpoint whose source is invisible is one nobody can weigh. It is also
	// what the duplicate check below reads, so two servers closing on the same
	// transcript do not both record it.
	if already, err := recorded(vaultDir, project, c.State); err != nil || already {
		return nil, err
	}
	written, err := session.WriteAuto(vaultDir, c)
	if err != nil {
		return nil, err
	}
	return &written, nil
}

// autoRecord is the record of what s did that its agent did not hand off, and
// false when that was no work at all.
func autoRecord(s *transcript.Session, project string, at int64) (session.Checkpoint, bool) {
	h := Harvest(afterHandoff(s))
	if len(h.Files) == 0 && len(h.Commands) == 0 {
		return session.Checkpoint{}, false
	}
	commands := h.Commands
	if n := len(commands); n > maxAutoCommands {
		commands = commands[n-maxAutoCommands:]
	}
	return session.Checkpoint{
		Project:  project,
		Agent:    s.Harness,
		Task:     firstPrompt(s),
		State:    autoState(s),
		Files:    h.Files,
		Commands: commands,
		TS:       at,
	}, true
}

// autoState is the provenance line an auto record carries. The shutdown path
// and the sweep must write it identically: it is the only thing that tells
// either one the other already recorded this transcript.
func autoState(s *transcript.Session) string {
	return fmt.Sprintf("Built from %s's own transcript (%s) when the session ended without a checkpoint.", s.Harness, s.ID)
}

// afterHandoff is the part of a session its agent has not already handed off:
// the turns after its last checkpoint or handoff call that went through.
//
// Read from the transcript because the transcript is the only thing that knows.
// The checkpoint a session wrote does not say which transcript it came from, so
// the sweep once took any checkpoint dated inside a session's span as that
// session's own — and a checkpoint from a second window open on the same
// project hid a session that was lost, while a session that checkpointed at
// noon and worked until six was taken as handed off in full.
func afterHandoff(s *transcript.Session) *transcript.Session {
	last := -1
	for i, t := range s.Turns {
		if isHandoffCall(t) {
			last = i
		}
	}
	if last < 0 {
		return s
	}
	rest := *s
	rest.Turns = s.Turns[last+1:]
	return &rest
}

// isHandoffCall is a checkpoint or handoff through Logos's MCP tool, under
// whatever name the host gives it, or through the CLI in a shell. Each host
// names an MCP tool its own way — Claude Code mcp__<server>__checkpoint, Codex
// the bare tool name — and the server's part is the user's choice, so the
// tool's own name is matched after any separator. A call that failed handed
// nothing off.
func isHandoffCall(t transcript.Turn) bool {
	if t.Role != "tool" || t.Status == "error" {
		return false
	}
	name := strings.ToLower(t.Tool)
	for _, verb := range []string{"checkpoint", "handoff"} {
		if name == verb {
			return true
		}
		if rest, ok := strings.CutSuffix(name, verb); ok && strings.ContainsAny(rest[len(rest)-1:], "_./:") {
			return true
		}
	}
	if isShellTool(t.Tool) {
		for _, verb := range []string{"logos checkpoint", "logos handoff", "brain checkpoint", "brain handoff"} {
			if strings.Contains(t.Input, verb) {
				return true
			}
		}
	}
	return false
}

// recorded reports whether the project's most recent checkpoint is the auto
// record for this same transcript. Only the latest is checked: this runs at
// host shutdown, so a match is the sibling server that closed a moment ago, and
// walking the whole project's history to rule out an ancient one would cost
// every shutdown for a case that cannot happen.
func recorded(vaultDir, project, state string) (bool, error) {
	prev, err := session.Latest(vaultDir, project)
	if err != nil || prev == nil {
		return false, err
	}
	return prev.Auto && prev.State == state, nil
}

// firstPrompt is what the person asked for, as the task. The first user turn is
// the only one that is reliably theirs: later ones may be answers to the
// agent's own questions.
func firstPrompt(s *transcript.Session) string {
	for _, t := range s.Turns {
		if t.Role != "user" {
			continue
		}
		if line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(t.Text), "\n", 2)[0]); line != "" {
			return text.Ellipsize(line, 120)
		}
	}
	return ""
}
