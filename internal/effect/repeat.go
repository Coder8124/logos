package effect

import (
	"regexp"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/textmatch"
	"github.com/Coder8124/logos/internal/transcript"
)

// A repeat correction is the person calling an agent back to something Logos
// already held when the session began. It is the failure a memory exists to
// prevent — the thing was written down, and they had to say it again — so the
// share of corrections that are repeats is the one number here that should
// fall as a vault learns its owner.
//
// Both halves are read without a model, and both are blunt. Which turns are
// corrections is decided by how they open; which of those restate something
// held is decided by shared words, at the bar before_you_try uses for "this is
// an approach somebody already ruled out". So the count is a list to read
// rather than a verdict, and a caller prints what each one matched.

// Known is one thing Logos held: a memory, or what a checkpoint recorded as
// decided or asked for.
type Known struct {
	Ref     string // how a person finds it: "memory 12", or a checkpoint's slug
	Text    string
	Project string // "" for one that applies to every project
	Since   int64  // when Logos first held it
}

// Held is what Logos knew, ready to be asked about.
type Held struct {
	items    []Known
	subjects []map[string]bool
	same     func(a, b string) bool
}

// Hold prepares known for matching. same says whether two project names are
// one project, since a caller may file a worktree under its repository; nil
// compares the names as given.
func Hold(known []Known, same func(a, b string) bool) Held {
	if same == nil {
		same = func(a, b string) bool { return a == b }
	}
	h := Held{same: same}
	for _, k := range known {
		subject := textmatch.Subject(k.Text)
		if len(subject) < 2 {
			// One distinctive word is not a statement anything can restate.
			continue
		}
		h.items = append(h.items, k)
		h.subjects = append(h.subjects, subject)
	}
	return h
}

// restated is the thing held before `before`, in project or everywhere, that
// text is most about, and nil when it is about none of them.
//
// A session with no project named — Cursor's transcripts carry none — is
// matched against what applies everywhere and nothing else. Guessing its
// project would let one repository's memory answer for another's correction,
// and a wrong match here is a failure reported that never happened.
func (h Held) restated(text, project string, before int64) *Known {
	if before <= 0 {
		return nil // undated: what was held "before" it cannot be said
	}
	asked := textmatch.Subject(text)
	best, bestScore, bestFit := -1, 0.0, 0.0
	for i, k := range h.items {
		if k.Since <= 0 || k.Since >= before {
			continue
		}
		if k.Project != "" && (project == "" || !h.same(k.Project, project)) {
			continue
		}
		// The dead-end check's bar, for its reason: half the shorter
		// statement's words is one word when the statement has two, and one
		// shared word is not the same subject.
		n := shared(asked, h.subjects[i])
		if n < 2 {
			continue
		}
		score := textmatch.Overlap(asked, h.subjects[i])
		if score < textmatch.Related {
			continue
		}
		// Overlap is containment in the shorter statement, so a long memory
		// that mentions the same two words scores as well as a short one that
		// is about them. Between equals, the one more of whose own words were
		// matched is the one named; then the one held longest.
		fit := float64(n) / float64(len(h.subjects[i]))
		if score > bestScore || (score == bestScore && (fit > bestFit || (fit == bestFit && k.Since < h.items[best].Since))) {
			best, bestScore, bestFit = i, score, fit
		}
	}
	if best < 0 {
		return nil
	}
	k := h.items[best]
	return &k
}

func shared(a, b map[string]bool) int {
	n := 0
	for w := range b {
		for v := range a {
			if textmatch.Akin(w, v) {
				n++
				break
			}
		}
	}
	return n
}

// Correction is one turn in which the person called the agent back.
type Correction struct {
	Harness, Session, Project string
	Turn                      int // its place in the session's turns
	// At is when it was written, or when its session began where the host
	// keeps no time on a turn.
	At int64
	// Known is what Logos already held that the correction restates, and nil
	// for a correction about something new.
	Known *Known
}

// Corrections lists the corrections in a session, each matched against what
// was held when the session began — not when the turn was written: a memory
// stored halfway through reached no agent that was already running.
func Corrections(s *transcript.Session, held Held) []Correction {
	var out []Correction
	asked, acted := false, false
	for i, t := range s.Turns {
		switch t.Role {
		case "assistant", "tool":
			acted = acted || asked
		case "user":
			text := ingest.PersonsText(t.Text)
			if text == "" {
				continue
			}
			// The first thing a person says is a request, however it is
			// phrased: there is nothing yet to call the agent back from.
			if !asked || !acted {
				asked = true
				continue
			}
			if !corrects(text) {
				continue
			}
			c := Correction{Harness: s.Harness, Session: s.ID, Project: s.Project, Turn: i, At: t.TS}
			if c.At == 0 {
				c.At = s.Started
			}
			c.Known = held.restated(text, s.Project, s.Started)
			out = append(out, c)
		}
	}
	return out
}

// opening is how much of a turn is read for a cue. A correction leads with
// itself; a long request that says "don't" in its fourth paragraph is a
// request with a condition in it.
const opening = 200

var (
	opensWith = regexp.MustCompile(`^[^a-z0-9]*(?:no|nope|not|don't|dont|do not|never|instead|undo|revert|wrong)\b`)
	// stops is "stop" said to the agent about what it is doing — "stop, …",
	// "stop editing the vendor directory", "stop that" — and not "stop" with
	// an object, which is a request: on the first real transcripts this was
	// run against, every "stop …" was "stop the service on port 8000".
	stops = regexp.MustCompile(`^[^a-z0-9]*stop(?:[,.!;:]|\s+(?:that|it|this|there|right|[a-z]+ing)\b)`)
	// notACue is an opening that starts like a correction and is not one.
	notACue = regexp.MustCompile(`^[^a-z0-9]*(?:no (?:problem|worries|rush)|not (?:sure|yet|bad)|never ?mind|don'?t you|do not you)\b`)
	// said are the phrasings that point back at something already said or
	// done. "instead of" and "rather than" are deliberately not among them:
	// they turn up in questions and plain requests as often as in corrections
	// ("why does it say brain instead of logos?").
	said = []string{
		"i said", "i told you", "i already", "as i said", "like i said",
		"i asked for", "i asked you", "not what i", "didn't ask", "did not ask",
		"don't do", "do not do",
		"you forgot", "you missed", "you didn't", "you did not",
		"shouldn't have", "should not have",
		"that's wrong", "that is wrong", "that's not", "that is not",
		"why did you", "why are you",
	}
)

// corrects reports whether a turn reads as the person calling the agent back
// and saying what they wanted.
//
// A list of openings, and so a floor: "hmm, could we use brew for this" is a
// correction this does not see. A bare "no" or "stop" is seen and left out —
// it names nothing, so there is nothing it could be found to restate, and
// counting it would dilute the rate with turns that could never be repeats.
//
// It has been checked once, and badly: against 217 turns of one person's
// transcripts marked by hand, six of them corrections, it found none and
// mistook none. All six were plain instructions — "use the config in the k8s
// directory", "replace this model with that one" — which open like any
// request and are corrections only beside what the agent had just done. So a
// count from it is a floor and a place to look, a caller says so, and
// TestTheListsAreScoredAgainstTurnsAPersonMarked is where a better list would show itself.
func corrects(text string) bool {
	low := strings.ToLower(strings.ReplaceAll(text, "’", "'"))
	if len(low) > opening {
		low = low[:opening]
	}
	if notACue.MatchString(low) {
		return false
	}
	cue := opensWith.MatchString(low) || stops.MatchString(low)
	for i := 0; !cue && i < len(said); i++ {
		cue = strings.Contains(low, said[i])
	}
	return cue && len(textmatch.Subject(text)) >= 2
}

// EnoughForRate is how many corrections it takes before their repeated share
// is worth a percentage. Under it the fraction is the honest form: "1 of 3"
// says how little it rests on, and "33%" hides it.
const EnoughForRate = 10

// Repeats is the corrections of many sessions, counted.
type Repeats struct {
	Sessions    int // sessions read
	Corrected   int // of those, the ones with a correction in them
	Corrections int
	Repeated    int
	// Each is the repeated ones, oldest first, for a person to read: the
	// match is by shared words and deserves a glance before it is believed.
	Each []Correction
}

// Add counts one session's corrections.
func (r *Repeats) Add(cs []Correction) {
	r.Sessions++
	if len(cs) > 0 {
		r.Corrected++
	}
	for _, c := range cs {
		r.Corrections++
		if c.Known != nil {
			r.Repeated++
			r.Each = append(r.Each, c)
		}
	}
	sort.SliceStable(r.Each, func(i, j int) bool { return r.Each[i].At < r.Each[j].At })
}

// Rate is the share of corrections that restated something held, and false
// when there are too few corrections for a share to mean anything.
func (r Repeats) Rate() (float64, bool) {
	if r.Corrections < EnoughForRate {
		return 0, false
	}
	return float64(r.Repeated) / float64(r.Corrections), true
}
