package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/spend"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// restoreSavings is what restoring a checkpoint spared, measured against the
// session it continued. A restore pack records sent == full, so the ledger's
// own saving for it is always zero; the real saving is that the agent read a
// small pack instead of reloading the session it picks up. That session's size
// is the host's to know, so it is read from the host's transcript at the time
// the dashboard is drawn — nothing is stored, and a ledger recorded before this
// measure existed gains it for free.
//
// Every restore lands in exactly one of Measured and the counts after it, so
// the lines printed from them always add up to Restores.
type restoreSavings struct {
	Restores int // restore packs in the window
	Measured int // those whose restored session has a transcript here
	Saved    int64
	// Sent and Baseline are per-restore medians over the measured ones.
	Sent, Baseline int64
	Daily          map[string]float64 // "2006-01-02" -> tokens, measured restores only

	// Why the rest were not measured. They are different problems with
	// different fixes, so they are never one "no transcript" count.
	NoCheckpoint  int // no checkpoint in the project before the restore
	NoHostSession int // the checkpoint does not say which host session wrote it
	NoTranscript  int // it does, and that session has no transcript on this machine
	SameSession   int // resumed inside the session that wrote the checkpoint: nothing was reloaded
	Repeats       int // a second restore of one checkpoint in one reader session
	Unread        int // could not be looked up because Err; see Err

	// Other is the non-restore packs, still measured the old way: against
	// their own uncut candidates.
	OtherPacks int
	OtherSaved int64
	Err        error // a history or the transcripts could not be read; the counts stay honest
}

// cutID reports whether id is the 8-hex-digit prefix the ledger and the
// session hooks keep in place of a full host session id. Hex, not just eight
// characters: a host's own id is not promised to be longer than that.
func cutID(id string) bool {
	if len(id) != 8 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// sameHostSession compares two host session ids when either may be an
// 8-character cut of the other.
func sameHostSession(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return a == b || (cutID(a) && strings.HasPrefix(b, a))
}

// measureRestores finds, for each restore in events, the checkpoint it handed
// over — the newest one in that event's scope at or before the event — and
// takes the largest context its host session had reached by the time that
// checkpoint was written as the baseline. Saved is baseline - sent, floored at
// zero: a restore of a session smaller than its pack saved nothing, and is not
// allowed to subtract from the total.
//
// A restore is credited once per (checkpoint, reader session): the session
// start hook and the agent's own resume in the same session are one reload
// spared, not two. A restore read by the very session that wrote the checkpoint
// reloaded nothing and is not credited at all.
func measureRestores(v string, events []usagepkg.Event, project string) restoreSavings {
	rs := restoreSavings{Daily: map[string]float64{}}
	type restore struct {
		e  usagepkg.Event
		cp session.Checkpoint
	}
	var found []restore
	// Keyed by the exact scope the event names, not its project: a worktree's
	// checkpoints are filed apart from its project's, and sharing one list
	// restored from whichever spelling the ledger happened to show first.
	history := map[string][]session.Checkpoint{}
	seen := map[string]bool{}
	cutoffs := map[string]int64{}
	oldest := int64(0)
	for _, e := range events {
		if e.Kind != usagepkg.KindPack || (project != "" && !usagepkg.SameProject(e.Project, project)) {
			continue
		}
		if !usagepkg.IsResume(e) {
			rs.OtherPacks++
			rs.OtherSaved += int64(max(e.Full-e.Sent, 0))
			continue
		}
		rs.Restores++
		scope := session.HistoryScope(v, e.Project)
		h, ok := history[scope]
		if !ok {
			var err error
			if h, err = session.History(v, e.Project, 0); err != nil {
				if rs.Err == nil {
					rs.Err = fmt.Errorf("reading %s's checkpoints: %w", e.Project, err)
				}
				rs.Unread++
				continue // not cached: the next event for it says the same
			}
			history[scope] = h
		}
		var cp *session.Checkpoint
		for i := range h {
			if h[i].TS <= e.TS {
				cp = &h[i]
				break
			}
		}
		switch {
		case cp == nil:
			rs.NoCheckpoint++
		case cp.HostSession == "" || cutID(cp.HostSession):
			// An 8-character cut cannot name a transcript: it matches no
			// full id, so it is as unlinked as an empty one.
			rs.NoHostSession++
		case sameHostSession(e.Session, cp.HostSession):
			rs.SameSession++
		default:
			if e.Session != "" {
				key := fmt.Sprintf("%s|%d|%s", scope, cp.TS, e.Session)
				if seen[key] {
					rs.Repeats++
					continue
				}
				seen[key] = true
			}
			found = append(found, restore{e, *cp})
			cutoffs[cp.HostSession] = 0
			if oldest == 0 || cp.TS < oldest {
				oldest = cp.TS
			}
		}
	}
	if len(found) == 0 {
		return rs
	}
	ids := make(map[string]bool, len(cutoffs))
	for id := range cutoffs {
		ids[id] = true
	}
	samples, err := spend.Samples(time.Unix(oldest, 0).Add(-time.Hour), ids)
	if err != nil {
		if rs.Err == nil {
			rs.Err = fmt.Errorf("reading the hosts' transcripts: %w", err)
		}
		rs.Unread += len(found)
		return rs
	}
	var sent, base []int64
	for _, f := range found {
		peak := spend.PeakAt(samples[f.cp.HostSession], f.cp.TS)
		if peak <= 0 {
			rs.NoTranscript++
			continue
		}
		rs.Measured++
		saved := max(peak-int64(f.e.Sent), 0)
		rs.Saved += saved
		rs.Daily[time.Unix(f.e.TS, 0).Format("2006-01-02")] += float64(saved)
		sent, base = append(sent, int64(f.e.Sent)), append(base, peak)
	}
	rs.Sent, rs.Baseline = median(sent), median(base)
	return rs
}

// notMeasured is one terse line per reason a restore went unmeasured, zero
// counts left out. The reasons are told apart because only the last is about
// this machine.
func (r restoreSavings) notMeasured() []string {
	var out []string
	add := func(n int, format string) {
		if n > 0 {
			out = append(out, fmt.Sprintf("%d restore%s %s", n, pluralS(n), format))
		}
	}
	add(r.NoCheckpoint, "had no earlier checkpoint")
	add(r.NoHostSession, "had a checkpoint that names no host session (written from a shell, or by an old server)")
	add(r.NoTranscript, "had no transcript on this machine")
	add(r.SameSession, "resumed inside the session that wrote the checkpoint, reloading nothing")
	add(r.Repeats, "repeated one already counted in the same session")
	add(r.Unread, "could not be measured")
	return out
}

func median(vs []int64) int64 {
	if len(vs) == 0 {
		return 0
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i] < vs[j] })
	return vs[len(vs)/2]
}
