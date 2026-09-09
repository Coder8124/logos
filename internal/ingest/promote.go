package ingest

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/Coder8124/brain/internal/gitstate"
	"github.com/Coder8124/brain/internal/session"
)

// Promote turns a reviewed candidate into a real checkpoint. This is the only
// path from the ingest queue into sessions/ — a candidate is never indexed as a
// checkpoint, and nothing here replays the transcript.
//
// The resulting checkpoint records where it came from: the harness, the
// harness-native session id and the source path go into its Decisions, and its
// git block is marked "(imported)" rather than being filled from whatever the
// working tree looks like now — the session being promoted did not run here.
//
// abs is the candidate note's path on disk; on success its status is rewritten
// to promoted so Pending stops offering it.
func Promote(db *sql.DB, vaultDir string, c Candidate, abs string) (*session.Checkpoint, error) {
	if c.Status == StatusPromoted {
		return nil, fmt.Errorf("candidate %s was already promoted", c.SessionID)
	}
	project := c.Project
	if strings.TrimSpace(project) == "" {
		return nil, fmt.Errorf(
			"candidate %s has no project — resolve it with review before promoting", c.SessionID)
	}

	prov := fmt.Sprintf("Imported from %s session %s", c.Harness, c.SessionID)
	if c.Source != "" {
		prov += " (" + c.Source + ")"
	}

	cp := &session.Checkpoint{
		Project:   project,
		Agent:     safeSegment(c.Harness),
		Task:      fmt.Sprintf("ingested %s session", c.Harness),
		State:     ingestState(c),
		Decisions: []string{prov},
		Verified:  c.Verified,
		Failed:    c.Failed,
		Blockers:  c.Blockers,
		Commands:  c.Commands,
		Files:     c.Files,
		Next:      c.Next,
		// Not the working tree — this session ran elsewhere. A non-empty block
		// stops Commit from reading git here and attributing it to the import.
		Git: gitstate.State{Branch: "(imported)", Commit: shortHash(c.Hash)},
	}
	if c.Ended > 0 {
		cp.TS = c.Ended
	}

	if err := session.Commit(db, vaultDir, cp); err != nil {
		return nil, err
	}
	if err := SetStatus(abs, c, StatusPromoted); err != nil {
		// The checkpoint is already written and durable; a failure to flip the
		// candidate's status is reported, not swallowed (invariant 4), but the
		// promotion stands.
		return cp, fmt.Errorf("checkpoint written to %s but candidate status not updated: %w", cp.Slug, err)
	}
	return cp, nil
}

// Reject marks a candidate rejected so Pending does not offer it again. The note
// stays on disk: the record that a session was seen and dismissed is itself
// worth keeping.
func Reject(abs string, c Candidate) error {
	if c.Status == StatusPromoted {
		return fmt.Errorf("candidate %s was already promoted; cannot reject", c.SessionID)
	}
	return SetStatus(abs, c, StatusRejected)
}

func ingestState(c Candidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s session %s, %d turns", c.Harness, c.SessionID, c.Turns)
	if c.Skipped > 0 {
		fmt.Fprintf(&b, " (%d unparsed line(s) skipped)", c.Skipped)
	}
	b.WriteString(".")
	if c.Tier == TierHarvest || c.Tier == "" {
		b.WriteString(" Harvest only — verified and failed were not distilled by a model.")
	}
	return b.String()
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
