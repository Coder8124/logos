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
	if len(args) > 0 && args[0] == "redact" {
		return runTranscriptRedact()
	}
	id := firstNonFlag(args)
	if id == "" {
		return fmt.Errorf("usage: logos transcript <session-id>")
	}
	v := vaultPath()
	if _, err := os.Stat(v); err != nil {
		return missingVaultError(v)
	}
	// A checkpoint's slug asks for the turns its lines came from, not the whole
	// conversation: the id form below stays what it was.
	if ingest.IsRecordRef(id) {
		return runTranscriptSpan(v, id)
	}
	p := session.FindTranscript(v, id)
	if p == "" {
		return fmt.Errorf("no transcript is archived for session %q — Logos keeps one when it reads a session's transcript, unless `logos config auto config archive off` (the id must match exactly)", id)
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

// runTranscriptSpan is `logos transcript <checkpoint-slug>`: print only the
// turns that checkpoint's lines came from. The span goes to stdout in the
// fenced, flattened form the evidence uses, because a turn is not a line of the
// host's file and piping a slice of raw JSONL would claim a precision the
// readers do not have. When the transcript is gone the exit is non-zero and the
// stdout empty: an empty span on a zero exit reads as "nothing was said".
func runTranscriptSpan(v, slug string) error {
	sp, c, err := ingest.SpanFor(v, slug)
	if err != nil {
		return err
	}
	fmt.Print(sp.Render("checkpoint " + c.Slug))
	return nil
}

// runTranscriptRedact is `logos transcript redact`: rewrite the archived copies
// that were kept raw — before redaction existed, or while `redact` was off —
// so they hold no secret and no <private> span. Atomic per copy, and it says
// the count, because a command that rewrote files and printed nothing would
// read as one that did nothing. A copy it cannot rewrite is named and the exit
// is non-zero: the vault is then not as clean as the count suggests.
func runTranscriptRedact() error {
	v := vaultPath()
	if _, err := os.Stat(v); err != nil {
		return missingVaultError(v)
	}
	raw, problems, err := ingest.RawCopies(v)
	if err != nil {
		return fmt.Errorf("scanning archived transcripts: %w", err)
	}
	var secrets, private, failed int
	rewritten := 0
	for _, r := range raw {
		st, err := ingest.RedactCopy(v, r.Path)
		if err != nil {
			failed++
			fmt.Printf("  not redacted  %s: %v\n", r.Path, err)
			continue
		}
		rewritten++
		secrets += st.Secrets
		private += st.Private
		fmt.Printf("  redacted  %s  (%d secret(s), %d <private> span(s))\n", r.Path, st.Secrets, st.Private)
	}
	for _, p := range problems {
		fmt.Printf("  unreadable  %s\n", p)
	}
	fmt.Printf("%d archived transcript(s) redacted (%d secret(s), %d <private> span(s) masked) · %d could not be redacted · %d unreadable\n",
		rewritten, secrets, private, failed, len(problems))
	if failed+len(problems) > 0 {
		return fmt.Errorf("%d archived transcript(s) are still raw", failed+len(problems))
	}
	return nil
}
