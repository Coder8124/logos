// Package effect measures what a restore did for the session that got it, from
// what the hosts themselves recorded of that session.
//
// The usage ledger can say a checkpoint was handed over and how large it was.
// It cannot say whether that helped, and the one figure that tried — the size
// handed over, priced at a typing speed — is an estimate of something nobody
// did. A transcript holds what was done: how much the person wrote and how
// much the agent looked up before the first file changed, each time the
// person came back because an attempt had not done the job, and each time
// they called the agent back to what they had wanted.
//
// Everything here is counted from a transcript and none of it is estimated.
// What a count means is the reader's to judge, and a caller that prints a
// comparison prints beside it why it is not an experiment: nobody assigned
// sittings to a restore at random, and one that got a checkpoint is carrying
// work on where a cold one is starting it.
//
// Reading is all this package does. It holds no transcript text in what it
// returns — counts, ids and times — so a report built from it can be shown
// without showing a conversation.
package effect

import (
	"regexp"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

// How a sitting began, as far as a restore goes.
const (
	Cold     = "cold"     // nothing was handed over before the first edit
	Restored = "restored" // a checkpoint was
	// Unclear is a resume the transcript shows being called with no result kept
	// beside it, so whether it handed over a checkpoint or said there was none
	// is not known. Counted, and in neither arm of a comparison.
	Unclear = "unclear"
)

// Start is how one sitting began: what it took to reach the first file edit.
//
// A sitting is a stretch of a session the person sat down to. The first one
// opens the session; each later one begins where they came back to it after a
// break. A session is not the unit because of how sessions are kept: on the
// machine this was first run on, four of them had been left open for months
// and reopened dozens of times, so "how sessions began" had four things to
// count and the person had begun work sixty times.
type Start struct {
	Harness, ID, Project string
	// Sitting is which of its session's sittings this is, 0 for the one that
	// opens it. A later one began with the host's own record of the
	// conversation still in front of the agent, which a session's first
	// sitting never has: the two are not compared with each other.
	Sitting int
	// First and End are the sitting's turns in its session: First up to, not
	// including, End.
	First, End int
	// After is when the last turn before this sitting was written, and 0 for
	// the sitting that opens the session.
	After int64
	Began int64 // when the sitting started, 0 where the host keeps no time
	// Restore is Cold, Restored or Unclear, read from the transcript alone. A
	// caller that holds the usage ledger knows of restores no transcript shows
	// — the one a session-start hook made before the first turn — and raises
	// Cold or Unclear to Restored from it.
	Restore string
	// Asks is the turns the person wrote before the first edit, and Words the
	// words in them: what they had to say before work began.
	Asks  int
	Words int
	// Lookups is the tool calls before the first edit, the resume itself left
	// out: what the agent read, searched and ran to find its footing.
	Lookups int
	// Edited is false for a session that never changed a file — a question, a
	// review — and its counts then run to its end. Such a session has no first
	// edit to measure up to and is left out of a comparison's medians.
	Edited bool
	// AskedAt and EditAt are when the person first wrote and when the first
	// edit landed, each 0 where the host keeps no time on a turn.
	AskedAt int64
	EditAt  int64
}

// Timed reports whether the time to the first edit is known.
func (s Start) Timed() bool { return s.Edited && s.AskedAt > 0 && s.EditAt >= s.AskedAt }

// Minutes is the time from the person's first turn to the first edit. It is
// wall-clock: it holds the agent's work and the person's pauses alike.
func (s Start) Minutes() float64 { return float64(s.EditAt-s.AskedAt) / 60 }

// Break is how long a session lies untouched before the person's next turn
// begins a new sitting: thirty minutes in which neither they nor the agent
// wrote anything. An agent working through a long task leaves no such gap,
// however long the person is away from it, and that is still one sitting — it
// is their coming back that is being found, not their attention.
//
// Thirty because the count does not turn on it: over the sessions this was
// first run on, any break from thirty minutes to two hours found nearly the
// same sittings (25, 24 and 22 of them at 30, 60 and 120), where five minutes
// found 41. A pause to read is shorter than that range and a night is longer.
const Break = 30 * 60

// Sittings measures how each sitting of a session began, oldest first. gap is
// the break, in seconds, that ends one; 0 reads the session as a single
// sitting, which is also what a host that keeps no time on a turn gets —
// Cursor's chats cannot be split, and each is one sitting however often it
// was come back to.
//
// A stretch nobody wrote in — a headless run, a subagent's transcript, the
// host's bookkeeping before the first prompt — is not a sitting.
func Sittings(s *transcript.Session, gap int64) []Start {
	var out []Start
	add := func(from, to int, after int64) {
		if st, ok := measure(s, from, to); ok {
			st.Sitting, st.After = len(out), after
			out = append(out, st)
		}
	}
	from, after := 0, int64(0)
	var last int64 // when the person or the agent last wrote
	for i, t := range s.Turns {
		person := t.Role == "user" && wrote(t.Text)
		if !person && t.Role != "assistant" && t.Role != "tool" {
			continue // the host's own text is not anyone being at the session
		}
		if person && gap > 0 && t.TS > 0 && last > 0 && t.TS-last >= gap {
			add(from, i, after)
			from, after = i, last
		}
		if t.TS > last {
			last = t.TS
		}
	}
	add(from, len(s.Turns), after)
	return out
}

// wrote reports whether a user turn is the person at the session: lines of
// their own, or a paste the host marks, which is what they showed the agent
// when they had nothing to add. One test for every reader here — Replies
// counted a bare paste as a turn of theirs while the sitting it fell in
// did not, so a sitting could be missing the very turn that was counted
// as a reply in it.
func wrote(text string) bool {
	return len(ingest.PersonsLines(text)) > 0 || len(ingest.PastedLines(text)) > 0
}

// measure reads how the sitting in turns from up to to began, and false for a
// stretch no person wrote in.
func measure(s *transcript.Session, from, to int) (Start, bool) {
	st := Start{Harness: s.Harness, ID: s.ID, Project: s.Project, First: from, End: to, Restore: Cold}
	if from == 0 {
		st.Began = s.Started
	}
	asked := false
	for _, t := range s.Turns[from:to] {
		switch t.Role {
		case "user":
			if !wrote(t.Text) {
				continue
			}
			words := len(strings.Fields(ingest.PersonsText(t.Text)))
			if !asked {
				asked, st.AskedAt = true, t.TS
				if st.Began == 0 {
					st.Began = t.TS
				}
			}
			st.Asks++
			st.Words += words
		case "tool":
			if how, ok := resumeCall(t); ok {
				// The first answer stands: a second resume that finds nothing
				// does not take back the checkpoint the first one handed over.
				if st.Restore == Cold {
					st.Restore = how
				}
				continue
			}
			if isEdit(t) {
				// An edit before the person's first turn is the host's or a
				// hook's, not the work they asked for. Returning here dropped
				// the whole stretch as one nobody wrote in, and with it the
				// session's replies and corrections.
				if !asked {
					continue
				}
				st.Edited, st.EditAt = true, t.TS
				return st, asked
			}
			if asked {
				st.Lookups++
			}
		}
	}
	return st, asked
}

// resumeCall reports whether a tool turn is a call to Logos's resume, and what
// it handed over. A failed call handed over nothing and the session went on
// cold.
func resumeCall(t transcript.Turn) (string, bool) {
	name := strings.ToLower(t.Tool)
	if !strings.Contains(name, "logos") || !strings.HasSuffix(name, "resume") {
		return "", false
	}
	switch {
	case t.Status == "error":
		return Cold, true
	case strings.TrimSpace(t.Text) == "":
		return Unclear, true
	case saysNoCheckpoint(t.Text):
		return Cold, true
	}
	return Restored, true
}

// noCheckpointLine is the line resume ends a checkpoint-less answer with, in
// the form Logos prints it: italic, and followed by what it means.
var noCheckpointLine = regexp.MustCompile(`(?m)^_` + regexp.QuoteMeta(session.NoCheckpointYet) + ` — this is context`)

// saysNoCheckpoint reports whether a resume's answer is Logos saying there was
// no checkpoint. Not a search for the sentence anywhere in it: a note, a
// decision or a dead end in the vault can quote it, and a resume that handed a
// checkpoint over would then be counted as cold.
func saysNoCheckpoint(answer string) bool { return noCheckpointLine.MatchString(answer) }

// isEdit reports whether a tool turn changed a file: an edit tool that did not
// fail, or a shell command that plainly writes one.
func isEdit(t transcript.Turn) bool {
	if t.Role != "tool" || t.Status == "error" {
		return false
	}
	if ingest.EditTool(t.Tool) {
		return true
	}
	return ingest.ShellTool(t.Tool) && shellWrites(t.Input)
}

var (
	quotedSpan = regexp.MustCompile(`'[^']*'|"(?:[^"\\]|\\.)*"`)
	// segments split a command at what ends one: a verb is only a verb where a
	// command starts, and "grep -rn apply_patch" has no command named that.
	segments  = regexp.MustCompile(`[;&|(\n]+`)
	writeVerb = regexp.MustCompile(`^\s*(?:(?:sudo|xargs|time|nohup|env)\s+)*(apply_patch|git\s+apply|patch|sed|perl|tee)(?:\s(.*))?$`)
	// inPlace is -i as a flag of its own, alone or in a cluster that ends in
	// it (-pi, -ni) or with a backup suffix (-i.bak): not the i inside
	// "perl -Mstrict".
	inPlace    = regexp.MustCompile(`(?:^|\s)(?:--in-place\S*|-[a-zA-Z]*i(?:\.\S*)?)(?:\s|$)`)
	patchP     = regexp.MustCompile(`(?:^|\s)-p\d*(?:\s|$)`)
	dryRun     = regexp.MustCompile(`(?:^|\s)--(?:check|dry-run)\b`)
	redirectTo = regexp.MustCompile(`(?:^|[^0-9&>-])>>?\s*([^\s&>|;]+)`)
)

// shellWrites reports whether a shell command writes a file, for the agent
// that edits with sed, a heredoc or a redirect instead of an edit tool.
//
// Narrow on purpose. A lookup taken for an edit ends the count early and makes
// a session look quicker to start than it was, so this reads only what cannot
// be anything else: a patch applied, an in-place sed or perl, tee, and output
// redirected somewhere other than /dev. Quoted text is taken out first, where
// a ">" is a comparison in somebody's awk; so is a heredoc's body, which is
// the file's content and not the command. A script that writes a file from
// inside itself is not seen, and that session counts as never having edited.
func shellWrites(cmd string) bool {
	if i := strings.Index(cmd, "<<"); i >= 0 {
		if j := strings.IndexByte(cmd[i:], '\n'); j >= 0 {
			cmd = cmd[:i+j]
		}
	}
	cmd = quotedSpan.ReplaceAllString(cmd, `""`)
	for _, seg := range segments.Split(cmd, -1) {
		if writesAsVerb(seg) {
			return true
		}
	}
	for _, m := range redirectTo.FindAllStringSubmatch(cmd, -1) {
		if !strings.HasPrefix(m[1], "/dev/") {
			return true
		}
	}
	return false
}

// writesAsVerb reports whether one command of a pipeline or list is a verb
// that writes a file. A patch run only to check that it applies, or to rehearse
// it, writes nothing.
func writesAsVerb(seg string) bool {
	m := writeVerb.FindStringSubmatch(seg)
	if m == nil {
		return false
	}
	verb, args := strings.Join(strings.Fields(m[1]), " "), m[2]
	switch verb {
	case "apply_patch":
		return true
	case "git apply":
		return !dryRun.MatchString(" " + args)
	case "patch":
		return patchP.MatchString(" "+args) && !dryRun.MatchString(" "+args)
	case "sed", "perl":
		return inPlace.MatchString(" " + args)
	}
	return strings.TrimSpace(args) != "" // tee
}

// Enough is how many edited sittings each arm needs before their medians are
// set against each other. Below it a median is a handful of anecdotes, and a
// difference between two of those is noise with a number on it.
const Enough = 5

// Group is the sittings that began one way.
type Group struct {
	Sittings int // sittings a person wrote in
	Edited   int // of those, the ones that changed a file; the medians are over these
	// Medians, not means: one session that opened with a pasted specification
	// would otherwise speak for all of them.
	Asks, Words, Lookups float64
	// Timed is how many of the edited sessions have a time on their turns, and
	// Minutes the median over those.
	Timed   int
	Minutes float64
}

// Comparison sets the sittings that began from a restore beside the ones that
// began with nothing handed over.
type Comparison struct {
	Restored, Cold Group
	// Unclear is the sessions in neither arm — see the constant.
	Unclear int
}

// Comparable reports whether both arms hold enough edited sessions for a
// difference between them to be worth printing.
func (c Comparison) Comparable() bool {
	return c.Restored.Edited >= Enough && c.Cold.Edited >= Enough
}

// BySitting compares like with like: the sittings that opened a session with
// each other, and the ones that picked a session up again with each other. In
// the first pair a restore is all the agent was given; in the second the host
// still held the conversation either way, and the restore is what was added.
func BySitting(starts []Start) (opened, pickedUp Comparison) {
	var first, later []Start
	for _, s := range starts {
		if s.Sitting == 0 {
			first = append(first, s)
		} else {
			later = append(later, s)
		}
	}
	return Compare(first), Compare(later)
}

// Compare groups starts by how they began.
func Compare(starts []Start) Comparison {
	var c Comparison
	var restored, cold []Start
	for _, s := range starts {
		switch s.Restore {
		case Restored:
			restored = append(restored, s)
		case Unclear:
			c.Unclear++
		default:
			cold = append(cold, s)
		}
	}
	c.Restored, c.Cold = group(restored), group(cold)
	return c
}

func group(starts []Start) Group {
	g := Group{Sittings: len(starts)}
	var asks, words, lookups, minutes []float64
	for _, s := range starts {
		if !s.Edited {
			continue
		}
		g.Edited++
		asks = append(asks, float64(s.Asks))
		words = append(words, float64(s.Words))
		lookups = append(lookups, float64(s.Lookups))
		if s.Timed() {
			minutes = append(minutes, s.Minutes())
		}
	}
	g.Asks, g.Words, g.Lookups = median(asks), median(words), median(lookups)
	g.Timed, g.Minutes = len(minutes), median(minutes)
	return g
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if n := len(s); n%2 == 1 {
		return s[n/2]
	} else {
		return (s[n/2-1] + s[n/2]) / 2
	}
}
