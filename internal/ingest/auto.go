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
	if s == nil {
		return nil, nil
	}
	h := Harvest(s)
	if len(h.Files) == 0 && len(h.Commands) == 0 {
		return nil, nil
	}

	// Provenance in the record itself, not only in a log: an unverified
	// checkpoint whose source is invisible is one nobody can weigh. It is also
	// what the duplicate check below reads, so two servers closing on the same
	// transcript do not both record it.
	state := fmt.Sprintf("Built from %s's own transcript (%s) when the session ended without a checkpoint.", s.Harness, s.ID)
	if already, err := recorded(vaultDir, project, state); err != nil || already {
		return nil, err
	}

	commands := h.Commands
	if n := len(commands); n > maxAutoCommands {
		commands = commands[n-maxAutoCommands:]
	}
	c := session.Checkpoint{
		Project:  project,
		Agent:    s.Harness,
		Task:     firstPrompt(s),
		State:    state,
		Files:    h.Files,
		Commands: commands,
	}
	written, err := session.WriteAuto(vaultDir, c)
	if err != nil {
		return nil, err
	}
	return &written, nil
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
