package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
	"github.com/Coder8124/logos/internal/vault"
)

// The sweep is the floor under the shutdown path (#134). The server records a
// session that ended without a checkpoint from a deferred call, which runs only
// when its process returns normally: a host that is killed, a machine that
// sleeps and never reaps it, a server that failed to start or had no vault —
// each lost the session outright, while its transcript sat complete on disk
// and nothing ever read it again. The next resume reads it instead. Late, but
// it needs no process that outlives a session and no hook on any host.
//
// It adds coverage, not judgement: what it writes is AutoCheckpoint's record,
// with verified, failed and next empty, because the next agent treats a failed
// entry as a paid-for ruling.

const (
	// sweepWindow is how far back a transcript can have ended and still be
	// swept. Older work is the review queue's (`logos ingest`), where a person
	// decides what it was; recording a month of sessions as checkpoints on the
	// first resume after an upgrade would bury the handoffs under file lists.
	sweepWindow = 7 * 24 * time.Hour
	// sweepQuiet is how long a transcript must have gone unwritten before it
	// counts as ended. The session calling resume is itself writing one, and so
	// may another window the user still has open.
	sweepQuiet = 30 * time.Minute
	// coverSlack is how long after its transcript's last write a checkpoint can
	// land and still be that session's own: the session-end hook, or an agent
	// checkpointing as the user closes the window.
	coverSlack = 10 * time.Minute
)

// RecentTranscripts lists the transcripts, across every harness installed here,
// last written between since and until that may be sessions of project, less
// those settled says are already recorded. A seam: tests need no real host on
// this machine.
var RecentTranscripts = func(project string, since, until time.Time, settled func(path string, lastWrite time.Time) bool) ([]*transcript.Session, []string) {
	var out []*transcript.Session
	var problems []string
	for _, h := range transcript.Harnesses() {
		paths, err := transcript.Sessions(h)
		if err != nil {
			// A harness that is not installed is the ordinary case, not a
			// problem worth a line on every resume.
			var skip *transcript.SkipReason
			if !errors.As(err, &skip) {
				problems = append(problems, fmt.Sprintf("%s: %v", h, err))
			}
			continue
		}
		open := transcript.StillOpen(h)
		for _, p := range paths {
			// Stat before parsing: a host keeps every transcript it ever
			// wrote, and reading them all on each resume is the cost that
			// matters here.
			if !transcript.MayBelongTo(h, p, project) {
				continue
			}
			fi, err := os.Stat(transcript.SourceFile(p))
			if err != nil || fi.ModTime().Before(since) || fi.ModTime().After(until) || settled(p, fi.ModTime()) || open(p) {
				continue
			}
			s, err := transcript.ReadFile(h, p)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", p, err))
				continue
			}
			if s.Ended == 0 {
				// The last write is the best end there is, and without one
				// the record could not be dated.
				s.Ended = fi.ModTime().Unix()
			}
			out = append(out, s)
		}
	}
	return out, problems
}

// Sweep records every recent session of project that ended without a
// checkpoint, and brings up to date the record of one that went on working
// after it was recorded. It returns the records it wrote, the ones it grew, and
// a line for each transcript it could not read or record; the caller announces
// all three (invariants 3 and 4).
func Sweep(vaultDir, project string, now time.Time) (wrote, grew []session.Checkpoint, problems []string) {
	project = bareProject(project)
	if project == "" {
		return nil, nil, nil
	}
	history, err := session.History(vaultDir, project, 0)
	if err != nil {
		return nil, nil, []string{fmt.Sprintf("reading %s's checkpoints: %v", project, err)}
	}
	// A transcript judged by an earlier sweep and not written since is settled
	// before it is read: one week of a single project's transcripts was 158 MB
	// here, most of it one session, and parsing it again on every resume cost a
	// second each time.
	cache := loadSwept(vaultDir)
	judged := cache[project]
	kept := map[string]int64{}
	settled := func(path string, lastWrite time.Time) bool {
		if at, ok := judged[path]; ok && at == lastWrite.UnixNano() {
			kept[path] = at
			return true
		}
		return false
	}
	found, problems := RecentTranscripts(project, now.Add(-sweepWindow), now.Add(-sweepQuiet), settled)

	var mine []*transcript.Session
	for _, s := range found {
		// The window again, on the session's own end: the file's time only
		// says it was touched, and a host touches old transcripts.
		if bareProject(s.Project) == project && s.Ended >= now.Add(-sweepWindow).Unix() && s.Ended <= now.Add(-sweepQuiet).Unix() {
			mine = append(mine, s)
		}
	}

	failed := map[*transcript.Session]bool{}
	for _, s := range mine {
		rec, done := recordOf(history, s)
		if done {
			continue
		}
		var c *session.Checkpoint
		if rec == nil {
			c, err = autoCheckpoint(vaultDir, s, project, s.Ended)
		} else {
			c, err = growRecord(vaultDir, history, *rec, s)
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s session %s: %v", s.Harness, s.ID, err))
			failed[s] = true
			continue
		}
		switch {
		case c == nil:
		case rec != nil && c.Slug == rec.Slug:
			grew = append(grew, *c)
			for i := range history {
				if history[i].Slug == c.Slug {
					history[i] = *c
				}
			}
		default:
			wrote = append(wrote, *c)
			history = append(history, *c)
		}
	}

	// Every transcript read and not failed on is judged: recorded, handed off,
	// empty, too old or someone else's. A session that failed to record is
	// left out so the next resume tries it again, and one written to since has
	// a new file time and is read again.
	for _, s := range found {
		if failed[s] {
			continue
		}
		if fi, err := os.Stat(transcript.SourceFile(s.Path)); err == nil {
			kept[s.Path] = fi.ModTime().UnixNano()
		}
	}
	if !maps.Equal(judged, kept) {
		cache[project] = kept
		saveSwept(vaultDir, cache)
	}
	return wrote, grew, problems
}

// The swept cache is .logos/swept.json: per project, each transcript the sweep
// has already judged, with the file time it had then. Most transcripts give no
// record — nothing was run, or a checkpoint covers them — so nothing in the
// vault says they were read, and without this every resume reparsed them: a
// quarter of a second here, on a hook with a ten-second limit. It is keyed by
// project because a Codex or Cursor path says nothing of whose session it
// holds. Losing it costs one slow resume and no record, since covered and the
// auto record's own state still stop a second one; that is why it may live
// beside the index rather than in the vault.
func sweptPath(vaultDir string) string { return filepath.Join(vaultDir, ".logos", "swept.json") }

func loadSwept(vaultDir string) map[string]map[string]int64 {
	out := map[string]map[string]int64{}
	if raw, err := os.ReadFile(sweptPath(vaultDir)); err == nil {
		// A cache that will not parse is rebuilt by this sweep; the cost of
		// that is the reads it was saving, not anything the user has.
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// saveSwept drops the error for the same reason: an unwritable cache makes
// the next resume slower, and says nothing about whether this one recorded
// what it should.
func saveSwept(vaultDir string, cache map[string]map[string]int64) {
	if raw, err := json.Marshal(cache); err == nil {
		_ = vault.WriteAtomic(sweptPath(vaultDir), raw)
	}
}

// recordOf finds the auto record the vault already has for a transcript, and
// reports whether it covers the whole session. Each path that records a
// session — the Claude Code session-end hook, the server's shutdown, an
// earlier sweep — names the transcript it came from in the record's state, so
// this is a match on the id, not a guess from when things were written. What
// the agent handed off itself is not looked for here: afterHandoff reads that
// from the transcript, which is the only place that says which session a
// checkpoint came from.
//
// A hook record written before the hook named its session is matched as the
// sweep used to match everything, by time: the one written as this session's
// harness closed. Without that, every Claude Code session the hook recorded in
// the week before an upgrade would be recorded again by the first resume
// after it.
func recordOf(history []session.Checkpoint, s *transcript.Session) (*session.Checkpoint, bool) {
	var rec *session.Checkpoint
	for i, c := range history {
		if !c.Auto {
			continue
		}
		if s.ID != "" && strings.Contains(c.State, "("+s.ID+")") && (rec == nil || c.TS > rec.TS) {
			rec = &history[i]
		}
		if c.State == session.ActivityLogState && c.Agent == s.Harness &&
			c.TS >= s.Ended && c.TS <= s.Ended+int64(coverSlack/time.Second) {
			return &history[i], true
		}
	}
	if rec == nil {
		return nil, false
	}
	return rec, s.Ended <= rec.TS
}

// growRecord brings rec up to date with a session that went on after it was
// recorded, and returns the record, or nil when nothing it did since is work.
//
// In place when it can be: the record keeps its file, so nothing that links to
// it loses it. Not when another checkpoint was written in between — records
// are ordered by their file's name, and a record rewritten to end after a
// checkpoint that sorts after it would put the older of the two on top in
// resume. A new record is written for the session then, which lists again
// whatever of rec's work came after the agent's last handoff: a repeated line
// is the lesser cost next to a history out of order.
func growRecord(vaultDir string, history []session.Checkpoint, rec session.Checkpoint, s *transcript.Session) (*session.Checkpoint, error) {
	c, ok := autoRecord(s, rec.Project, s.Ended)
	if !ok || (slices.Equal(c.Files, rec.Files) && slices.Equal(c.Commands, rec.Commands)) {
		return nil, nil
	}
	for _, h := range history {
		if h.Slug != rec.Slug && h.TS > rec.TS && h.TS <= s.Ended {
			return autoCheckpoint(vaultDir, s, rec.Project, s.Ended)
		}
	}
	grown, err := session.GrowAuto(vaultDir, rec, c)
	if err != nil {
		return nil, err
	}
	return &grown, nil
}

// transcriptID is a transcript's id as its path gives it: the part after a
// Cursor source's '#', or the file's name without its extension.
func transcriptID(path string) string {
	if i := strings.LastIndexByte(path, '#'); i >= 0 {
		return path[i+1:]
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// bareProject is the project half of a scope. A transcript names the folder it
// ran in, and resume may be asked for a worktree scope beneath it.
func bareProject(p string) string {
	p, _, _ = strings.Cut(session.SafeScope(p), "/")
	return p
}

// SweepNotice is what resume says about a sweep, or "" when there is nothing
// to say. Both resumes print it: a checkpoint that appears in the history with
// nobody having written one reads as a bug, and a transcript that could not be
// read is a session that may still be lost.
func SweepNotice(wrote, grew []session.Checkpoint, problems []string) string {
	var b strings.Builder
	ids := func(cs []session.Checkpoint) string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = c.Session
		}
		return strings.Join(out, ", ")
	}
	if n := len(wrote); n > 0 {
		fmt.Fprintf(&b, "_Recorded %d earlier session%s that ended without a checkpoint, from the host's own transcript — auto, unverified, files and commands only: %s_\n\n",
			n, plural(n), ids(wrote))
	}
	// A record that changed under a reader with nobody having touched it reads
	// as the vault being unstable, so this says which, and why.
	if n := len(grew); n > 0 {
		fmt.Fprintf(&b, "_Brought %d auto record%s up to date with work its session did after it was recorded — still auto, unverified: %s_\n\n",
			n, plural(n), ids(grew))
	}
	if n := len(problems); n > 0 {
		// A few are enough to act on; a broken harness can fail on every file
		// it ever wrote, and the list would bury the handoff.
		shown := problems
		if len(shown) > 3 {
			shown = shown[:3]
		}
		more := ""
		if n > len(shown) {
			more = fmt.Sprintf("; and %d more", n-len(shown))
		}
		fmt.Fprintf(&b, "_Could not check %d transcript source%s for sessions that ended without a checkpoint: %s%s_\n\n",
			n, plural(n), strings.Join(shown, "; "), more)
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
