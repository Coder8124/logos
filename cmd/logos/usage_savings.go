package main

import (
	"sort"
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
type restoreSavings struct {
	Restores   int // restore packs in the window
	Measured   int // those whose restored session has a transcript here
	NoBaseline int // the rest: no checkpoint found, or no transcript on this machine
	Saved      int64
	// Sent and Baseline are per-restore medians over the measured ones.
	Sent, Baseline int64
	Daily          map[string]float64 // "2006-01-02" -> tokens, measured restores only

	// Other is the non-restore packs, still measured the old way: against
	// their own uncut candidates.
	OtherPacks int
	OtherSaved int64
	Err        error // the transcripts could not be read; Restores still counts
}

// measureRestores finds, for each restore in events, the checkpoint it handed
// over — the newest one in that event's project at or before the event — and
// takes the largest context its host session reached as the baseline. Saved is
// baseline - sent, floored at zero: a restore of a session smaller than its
// pack saved nothing, and is not allowed to subtract from the total.
func measureRestores(v string, events []usagepkg.Event, project string) restoreSavings {
	rs := restoreSavings{Daily: map[string]float64{}}
	type restore struct {
		e       usagepkg.Event
		session string
	}
	var found []restore
	history := map[string][]session.Checkpoint{} // per project, newest first
	oldest := int64(0)
	ids := map[string]bool{}
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
		key := usagepkg.ProjectKey(e.Project)
		h, ok := history[key]
		if !ok {
			h, _ = session.History(v, e.Project, 0) // an unreadable history is "no checkpoint found" below
			history[key] = h
		}
		for _, c := range h {
			if c.TS <= e.TS {
				if c.HostSession != "" {
					found = append(found, restore{e, c.HostSession})
					ids[c.HostSession] = true
					if oldest == 0 || c.TS < oldest {
						oldest = c.TS
					}
				}
				break
			}
		}
	}
	rs.NoBaseline = rs.Restores - len(found)
	if len(found) == 0 {
		return rs
	}
	peaks, err := spend.Peaks(time.Unix(oldest, 0).Add(-time.Hour), ids)
	if err != nil {
		rs.Err = err
		rs.NoBaseline = rs.Restores
		return rs
	}
	var sent, base []int64
	for _, f := range found {
		peak, ok := peaks[f.session]
		if !ok || peak <= 0 {
			rs.NoBaseline++
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

func median(vs []int64) int64 {
	if len(vs) == 0 {
		return 0
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i] < vs[j] })
	return vs[len(vs)/2]
}
