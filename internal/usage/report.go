package usage

import (
	"sort"
	"strings"
	"time"
)

// DefaultWPM is the typing speed the time-saved estimate assumes. It is an
// assumption, not a measurement — see RetypeMinutes — and `--wpm` replaces it.
const DefaultWPM = 40

// Tokens, characters and words, at the estimate contextpack counts with: about
// four characters to a token and five to a word.
const (
	charsPerToken = 4
	charsPerWord  = 5
)

// CheckpointStamp is the part of a checkpoint the report counts. The caller
// reads checkpoints from the vault, which is where they are written, so a
// month's report covers checkpoints made before anyone asked for one: the
// ledger holds packs and dead ends, not checkpoints.
type CheckpointStamp struct {
	TS      int64
	Project string
	Agent   string
	// Auto is a record built from the activity log when a session ended without
	// one. Counted apart: it says what ran, and nobody wrote it.
	Auto bool
	// HandoffTo is who a checkpoint was left for, empty for a plain one.
	HandoffTo string
}

// RetypeMinutes is the time a person would have spent typing out what a resume
// handed over, at wpm words a minute.
//
// This is the only "time saved" Logos can put a number on, and it is an
// estimate of the alternative, not a measurement of anything that happened:
// the alternative is a summary written or pasted by hand, and Logos never sees
// how long that took. It prices the context's size, and leaves out the harder
// half of the saving, the dead end that is not re-tried. A report that shows
// this figure must show how it was made.
func RetypeMinutes(tokens, wpm int) float64 {
	if tokens <= 0 || wpm <= 0 {
		return 0
	}
	words := float64(tokens) * charsPerToken / charsPerWord
	return words / float64(wpm)
}

// Kind of restore, by who wrote the checkpoint and who read it.
const (
	CrossAgent   = "cross-agent"  // another agent wrote it: claude, then cursor
	SameAgent    = "same-agent"   // the same agent, in a later session
	Unattributed = "unattributed" // the writer or the reader is not named
)

// RestoreKind classifies a resume. Both names must be known: a shell that was
// never told who is asking signs itself "cli", and counting that as different
// from every agent would make each person's own resume a handoff.
func RestoreKind(e Event) string {
	if !isResume(e) {
		return ""
	}
	known := func(n string) bool { return n != "" && n != "cli" }
	if !known(e.From) || !known(e.Reader) {
		return Unattributed
	}
	if e.Reader == e.From {
		return SameAgent
	}
	return CrossAgent
}

// SelfTest reports a resume `logos doctor` made of its own probe checkpoint.
// It shows the server answers; it is nobody's work handed over, and counting
// it put three "handoffs within one agent" on a month that had none of them.
func SelfTest(e Event) bool {
	return isResume(e) && (e.Reader == doctorAgent || e.From == doctorAgent)
}

// doctorAgent is the name internal/health's probe signs its checkpoint with.
const doctorAgent = "logos-doctor"

// HandedOver is the host sessions a resume handed a checkpoint to, each with
// every time it did, oldest first, keyed by the short id the ledger files a
// session under. Every time, because a session left open is handed a
// checkpoint again each time it is reopened, and which of its sittings began
// from one is a question about when.
//
// It knows only the resumes a hook made, since a hook is told its host's
// session and a server is not: a resume an agent called over MCP is in the
// ledger with no session beside it, and is found in that session's transcript
// instead. A pack names its writer only when it carried a checkpoint, so a
// resume that found nothing to hand over is not here.
func HandedOver(events []Event) map[string][]int64 {
	out := map[string][]int64{}
	for _, e := range events {
		if !isResume(e) || e.Session == "" || e.From == "" || SelfTest(e) {
			continue
		}
		out[e.Session] = append(out[e.Session], e.TS)
	}
	for _, at := range out {
		sort.Slice(at, func(i, j int) bool { return at[i] < at[j] })
	}
	return out
}

// Day is one calendar day, in the reader's time zone.
type Day struct {
	Date string // 2006-01-02

	// Checkpoints are the ones an agent wrote, Auto the ones built from the
	// activity log, and Declared the checkpoints written as a handoff to
	// someone, a subset of Checkpoints.
	Checkpoints int
	Auto        int
	Declared    int

	// Restores are the resumes that handed a checkpoint over, by kind.
	Restores     int
	Cross        int
	Same         int
	Unattributed int

	// Minutes is the estimate, by kind: RetypeMinutes of each pack's tokens.
	CrossMinutes float64
	SameMinutes  float64
}

// Pair is one writer and reader, and what passed between them.
type Pair struct {
	From, Reader string
	Restores     int
	Minutes      float64
}

// Report is a window of days and what happened in each.
type Report struct {
	From, To time.Time
	WPM      int
	Days     []Day
	Total    Day
	// Cross and Same are per pair of agents, largest first: "claude to
	// cursor" for work that crossed tools, "claude to claude" for work that
	// carried on within one.
	Cross []Pair
	Same  []Pair
	Each  []CheckpointStamp // every checkpoint in the window, oldest first
	// SelfTests is the resumes in the window that were `logos doctor` testing
	// itself. Left out of every count above, and said so rather than dropped.
	SelfTests int
}

// Build reports from..to, both inclusive days, for one project or for all of
// them when project is "". Every day in the window gets a row, empty or not,
// so a quiet day reads as quiet rather than missing.
func Build(events []Event, stamps []CheckpointStamp, from, to time.Time, project string, wpm int) Report {
	if wpm <= 0 {
		wpm = DefaultWPM
	}
	loc := from.Location()
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, loc)
	r := Report{From: start, To: end, WPM: wpm}

	idx := map[string]int{}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		date := d.Format("2006-01-02")
		idx[date] = len(r.Days)
		r.Days = append(r.Days, Day{Date: date})
	}
	dayOf := func(ts int64) (*Day, bool) {
		i, ok := idx[time.Unix(ts, 0).In(loc).Format("2006-01-02")]
		if !ok {
			return nil, false
		}
		return &r.Days[i], true
	}
	inProject := func(p string) bool { return project == "" || SameProject(p, project) }

	for _, s := range stamps {
		d, ok := dayOf(s.TS)
		if !ok || !inProject(s.Project) {
			continue
		}
		if s.Auto {
			d.Auto++
		} else {
			d.Checkpoints++
			if strings.TrimSpace(s.HandoffTo) != "" {
				d.Declared++
			}
		}
		r.Each = append(r.Each, s)
	}
	sort.SliceStable(r.Each, func(i, j int) bool { return r.Each[i].TS < r.Each[j].TS })

	cross, same := map[[2]string]*Pair{}, map[[2]string]*Pair{}
	add := func(m map[[2]string]*Pair, e Event, mins float64) {
		k := [2]string{e.From, e.Reader}
		p := m[k]
		if p == nil {
			p = &Pair{From: e.From, Reader: e.Reader}
			m[k] = p
		}
		p.Restores++
		p.Minutes += mins
	}
	for _, e := range events {
		kind := RestoreKind(e)
		if kind == "" || !inProject(e.Project) {
			continue
		}
		d, ok := dayOf(e.TS)
		if !ok {
			continue
		}
		if SelfTest(e) {
			r.SelfTests++
			continue
		}
		d.Restores++
		mins := RetypeMinutes(e.Sent, wpm)
		switch kind {
		case CrossAgent:
			d.Cross++
			d.CrossMinutes += mins
			add(cross, e, mins)
		case SameAgent:
			d.Same++
			d.SameMinutes += mins
			add(same, e, mins)
		default:
			d.Unattributed++
		}
	}

	for _, d := range r.Days {
		r.Total.Checkpoints += d.Checkpoints
		r.Total.Auto += d.Auto
		r.Total.Declared += d.Declared
		r.Total.Restores += d.Restores
		r.Total.Cross += d.Cross
		r.Total.Same += d.Same
		r.Total.Unattributed += d.Unattributed
		r.Total.CrossMinutes += d.CrossMinutes
		r.Total.SameMinutes += d.SameMinutes
	}
	r.Cross, r.Same = ranked(cross), ranked(same)
	return r
}

func ranked(m map[[2]string]*Pair) []Pair {
	out := make([]Pair, 0, len(m))
	for _, p := range m {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Restores != out[j].Restores {
			return out[i].Restores > out[j].Restores
		}
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].Reader < out[j].Reader
	})
	return out
}
