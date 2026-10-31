package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/session"
)

// runTranscript is `logos transcript <session-id>`: print the copy of a host
// transcript a harvest archived into the vault.
//
// Only the vault's copy, never the host's file, even when the host still has
// it: the point of the archive is a record that does not depend on the host,
// and a command that quietly read ~/.claude when it could would hide the day
// the archive was missing until the day the host's file was gone too.
//
// The id alone, as `logos resume <session-id>` takes it: a conversation's
// folder can sit under a project or any of its worktree scopes, and making
// the caller name which would make them know the layout this exists to hide.
// Matched exactly — see session.FindTranscript on why a near match is worse
// than a miss.
//
// The transcript goes to stdout untouched, so it pipes into jq or a pager the
// way the host's own file would; where it came from goes to stderr, so the
// line announcing it never lands inside what is being piped.
func runTranscript(args []string) error {
	id := firstNonFlag(args)
	if id == "" {
		return fmt.Errorf("usage: logos transcript <session-id>")
	}
	v := vaultPath()
	if _, err := os.Stat(v); err != nil {
		return missingVaultError(v)
	}
	p := session.FindTranscript(v, id)
	if p == "" {
		return fmt.Errorf("no transcript is archived for session %q — a harvest keeps one only after `logos import --from transcripts --keep-transcripts` (the id must match exactly)", id)
	}
	f, err := os.Open(p)
	if err != nil {
		return fmt.Errorf("reading the archived transcript %s: %w", p, err)
	}
	defer f.Close()
	rel := p
	if r, err := filepath.Rel(v, p); err == nil {
		rel = filepath.ToSlash(r)
	}
	if info, err := f.Stat(); err == nil {
		fmt.Fprintf(os.Stderr, "logos: transcript for %s, archived at %s (%s)\n", id, rel, ingest.Size(info.Size()))
	}
	if _, err := io.Copy(os.Stdout, f); err != nil {
		return fmt.Errorf("printing the archived transcript %s: %w", rel, err)
	}
	return nil
}
