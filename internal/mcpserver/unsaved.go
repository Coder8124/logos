package mcpserver

import (
	"fmt"
	"os"

	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/transcript"
)

// hooked lists the hosts that record their own sessions. Claude Code's
// session-end hook already builds this record from the activity log; writing it
// here as well would record the session twice.
var hooked = map[string]bool{"claude-code": true}

// newestSession reads the most recent transcript a harness has written. A seam:
// tests need no real Cursor on this machine.
var newestSession = func(harness string) (*transcript.Session, error) {
	paths, err := transcript.Sessions(harness)
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	return transcript.ReadFile(harness, paths[0])
}

// recordUnsaved writes a checkpoint for a session that did work and never saved
// one, built from the host's own transcript, and is called when stdin closes.
//
// This is the only route to continuity in a host with no hooks. The server sees
// calls to Logos's own tools and nothing else — not the host's edits, not its
// commands — so it has nothing of its own to reconstruct a session from. The
// transcript the host writes for itself has all of it and needs no cooperation
// from the host to read.
//
// Every failure here is reported on stderr and none of them is fatal: the host
// is already gone, and a shutdown that fails loudly is still a shutdown.
func (s *Session) recordUnsaved() {
	// The agent saved its own, which says what it verified and what it ruled
	// out. A mechanical record written after it would outrank it by being
	// newer, and replace a reviewed handoff with a list of files.
	if s.lastCheckpoint.slug != "" {
		return
	}
	if s.vault == "" || s.unavailable != nil {
		return
	}
	harness := s.clientAgent
	if harness == "" || hooked[harness] {
		return
	}

	ts, err := newestSession(harness)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logos: could not read %s's transcript to record this session: %v\n", harness, err)
		return
	}
	if ts == nil || !ranDuring(ts, s.startedAt.Unix()) {
		return
	}

	project := s.resolveScope(ts.Project)
	if project == "" {
		fmt.Fprintf(os.Stderr, "logos: this session did work and saved no checkpoint, and no project could be inferred to record it under\n")
		return
	}
	c, err := ingest.AutoCheckpoint(s.vault, ts, project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logos: could not record this session: %v\n", err)
		return
	}
	if c != nil {
		// Announced even though the host has gone: this is the line that tells
		// anyone reading the host's server log why a checkpoint they did not
		// write is in the vault (invariant 3).
		fmt.Fprintf(os.Stderr, "logos: this session ended without a checkpoint — recorded %s.md from %s's transcript, unverified\n", c.Slug, harness)
	}
}

// ranDuring reports whether a transcript was still being written while this
// server was alive. The newest transcript on disk is not necessarily this
// session's — a window the user closed an hour ago left one too, and harvesting
// that would attribute somebody else's work to a session that did nothing.
func ranDuring(ts *transcript.Session, serverStart int64) bool {
	// An unknown end is treated as still open rather than as long past: a
	// harness that does not stamp one would otherwise never be recorded.
	if ts.Ended == 0 {
		return ts.Started == 0 || ts.Started >= serverStart-openSessionSlack
	}
	return ts.Ended >= serverStart
}

// openSessionSlack allows for a host that starts its session a moment before it
// launches the MCP server it is configured with.
const openSessionSlack = 300
