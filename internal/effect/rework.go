package effect

import (
	"regexp"
	"strings"

	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/textmatch"
	"github.com/Coder8124/logos/internal/transcript"
)

// Rework is the person coming back because the last attempt did not do the
// job: the agent said it was done, or tried, and their next turn says it is
// still broken. It is what a session costs after the first answer, and the
// part of it a recorded dead end exists to prevent — the fix that was already
// tried, tried again.
//
// It is read from the person's turn alone, three ways, and none of them is a
// model. The lists were built in rounds against 217 turns of one person's
// transcripts that were marked by hand before any list was written, two
// thirds to tune on and a third held back: each round was kept only if it
// made fewer mistakes, and a second set — turns written from how people talk
// to an agent in general — was added when the first round turned out to have
// learned one person's vocabulary, in which every mention of an error was a
// report of one. On the third held back, scored once at the end, it found 25
// of 27 and was wrong about 2 of the 27 it named. That is one person and one
// reader's marks; TestTheListsAreScoredAgainstTurnsAPersonMarked scores it against anyone else's.

// How a rework turn was recognised.
const (
	// Said is in the person's own words: it is still failing, it did not
	// help, it should have done one thing and did another.
	Said = "said"
	// Pasted is an error or a failing log pasted back, with words beside it
	// or none.
	Pasted = "pasted"
	// Again is the request before it sent again, or a bare "try again". The
	// least sure of the three — right 3 times in 5 on the turns held back —
	// since a person may repeat a request for reasons of their own.
	Again = "again"
)

// Reply is one turn the person wrote, or pasted, after the agent had acted:
// a turn that could have been rework.
type Reply struct {
	Harness, Session, Project string
	Turn                      int // its place in the session's turns
	// At is when it was written, or when its session began where the host
	// keeps no time on a turn.
	At int64
	// Rework is how the turn says the last attempt did not do the job — Said,
	// Pasted or Again — and "" for a turn that does not.
	Rework string
}

// Replies lists the turns of a session that could have been rework, each with
// whether it was. The first thing a person says is not among them, however it
// is worded: there is nothing yet for it to be coming back from.
func Replies(s *transcript.Session) []Reply {
	var out []Reply
	asked, acted := false, false
	var before map[string]bool // the subject of the person's turn before this one
	for i, t := range s.Turns {
		switch t.Role {
		case "assistant", "tool":
			acted = acted || asked
		case "user":
			// What they wrote and what they pasted: an error pasted back with
			// no word beside it is the whole turn, and where the host marks a
			// paste the person's own lines do not hold it.
			lines, shown := ingest.PersonsLines(t.Text), ingest.PastedLines(t.Text)
			if len(lines) == 0 && len(shown) == 0 {
				// A request the person cut short themselves and sent again is
				// not the agent failing at it: the host's note that they did
				// comes as a turn of its own, between the two.
				if strings.Contains(t.Text, cutShort) {
					before = nil
				}
				continue
			}
			subject := textmatch.Subject(strings.Join(lines, " "))
			prev := before
			before = subject
			if strings.Contains(t.Text, cutShort) {
				before = nil
			}
			if !asked || !acted {
				asked = true
				continue
			}
			r := Reply{Harness: s.Harness, Session: s.ID, Project: s.Project, Turn: i, At: t.TS, Rework: reworks(lines, subject, prev)}
			if r.Rework == "" && showsFailure(shown) {
				r.Rework = Pasted
			}
			if r.At == 0 {
				r.At = s.Started
			}
			out = append(out, r)
		}
	}
	return out
}

// Reworked is the replies of many sessions, counted.
type Reworked struct {
	Sessions   int // sessions with a reply in them
	WithRework int // of those, the ones with rework in them
	Replies    int
	Rework     int
	By         map[string]int // rework, by how it was recognised
}

// Add counts one session's replies.
func (r *Reworked) Add(replies []Reply) {
	if len(replies) == 0 {
		return
	}
	r.Sessions++
	any := false
	for _, p := range replies {
		r.Replies++
		if p.Rework == "" {
			continue
		}
		any = true
		r.Rework++
		if r.By == nil {
			r.By = map[string]int{}
		}
		r.By[p.Rework]++
	}
	if any {
		r.WithRework++
	}
}

// Rate is the share of replies that were rework, and false when there are too
// few replies for a share to mean anything.
func (r Reworked) Rate() (float64, bool) {
	if r.Replies < EnoughForRate {
		return 0, false
	}
	return float64(r.Rework) / float64(r.Replies), true
}

// cutShort opens the line Claude Code writes when the person stops the agent.
const cutShort = "[Request interrupted by user"

var (
	// output is a line that came out of a program and not out of a person,
	// and reports something wrong.
	output = regexp.MustCompile(`(?i)traceback \(most recent call last\)|^\W*(?:error|warning|fatal)\b\W|"level": ?"(?:error|warning)"|\b[45]\d\d (?:unprocessable|internal|bad|not found|forbidden|unauthorized|gateway|service)|\w(?:error|exception|warning):|\bfailed:|command not found|no such file|\.\w{1,4}:\d+:`)

	// stillFine is "still" about something other than a problem being there:
	// what the person still wants, what the agent still has.
	stillFine = regexp.MustCompile(`\bstill (?:need|needs|want|wants|have|has|there|open|plan|think|prefer|like|pending)\b|\b(?:do|are|can|could|would|will) (?:you|we|i) still\b`)
	// persists: the problem is still there.
	persists = regexp.MustCompile(`\b(?:still|same (?:error|issue|problem|thing|result)|persist\w*|in ?spite of|repeatedly|as before)\b`)
	// plainly are the phrasings that are a report on their own.
	plainly = regexp.MustCompile(`\b(?:unable to|not able to|not working|no luck|nothing (?:happens|happened|works|worked|changed|shows))\b|(?:does|did)(?:n't| not) (?:work|help)\b`)
	// negated is something that should have happened and did not: a negation
	// that belongs to a subject — "it didn't", "is not", "I don't" — and then
	// an outcome within a few words. A bare "don't" or "do not" is an
	// instruction, and is not among them.
	negated = regexp.MustCompile(`(?:\b(?:does|did|is|are|was|were|wo|ca|could|would|has|have|had)n't|\b(?:does|did|is|are|am|was|were|will|can|could|would|has|have|had|it|its|it's|i'm|still|now|just) not|\b(?:i|it|we|they|this|that|which) (?:don't|do not|never)|\bcannot|\bwithout|^\W*not)\b(?: \w+){0,2}? (?:work\w*|load\w*|run\w*|start\w*|see\w*|able|reach\w*|respond\w*|generat\w*|show\w*|open\w*|connect\w*|resolv\w*|fix\w*|help\w*|build\w*|deploy\w*|install\w*|find\w*|complet\w*|finish\w*|available|real|actual|correct|right|compil\w*|pick\w*|pass\w*|updat\w*|appear\w*|display\w*|render\w*|sav\w*|apply\w*|appl\w*|chang\w*|creat\w*|return\w*|get\w*|receiv\w*|send\w*|trigger\w*|stop\w*|restart\w*)\b`)
	// expected is what it was meant to do, and then what it did.
	expected = regexp.MustCompile(`\b(?:suppos\w+ to|expected|should)\b.*\bbut (?:it|i|got|get|getting|instead|is|was|still|now|the|its|it's)\b`)

	clause   = regexp.MustCompile(`[.;:!?,]+`)
	wordLike = regexp.MustCompile(`[a-z0-9']+`)
	// wentWrong is a word for something having gone wrong.
	wentWrong = regexp.MustCompile(`^(?:error\w*|fail\w*|crash\w*|broken|exception\w*|traceback|blank|stuck|hang\w*|timeout\w*|wrong)$`)
)

// asVocabulary is what follows a word like "error" when it names a thing in
// the code and not something that happened: error handling, an error page.
var asVocabulary = map[string]bool{
	"handling": true, "handler": true, "handlers": true, "variable": true, "class": true, "classes": true,
	"type": true, "types": true, "boundary": true, "case": true, "cases": true, "page": true, "pages": true,
	"code": true, "codes": true, "path": true, "paths": true,
}

// toBuild are the verbs of an instruction to make something: "add a timeout",
// "return an error if the file is missing", "write a test for the failure".
// In their bare form only — "it throws an error" is a report.
var toBuild = map[string]bool{
	"add": true, "handle": true, "catch": true, "raise": true, "throw": true, "return": true, "log": true,
	"write": true, "create": true, "implement": true, "show": true, "display": true, "rename": true,
	"define": true, "mock": true, "simulate": true, "ignore": true, "suppress": true, "make": true,
	"test": true, "retry": true, "explain": true, "describe": true,
}

// reported are the verbs that put "an error" in front of the person: "I'm
// getting an error", "there is an exception". Without one, "an error" is any
// error — "what is the difference between a crash and a hang?".
var reported = map[string]bool{
	"get": true, "gets": true, "got": true, "getting": true, "see": true, "sees": true, "saw": true, "seeing": true,
	"have": true, "has": true, "had": true, "having": true, "hit": true, "hits": true, "hitting": true,
	"throws": true, "threw": true, "thrown": true, "throwing": true, "gives": true, "gave": true, "giving": true,
	"shows": true, "showed": true, "showing": true, "returns": true, "returned": true, "returning": true,
	"with": true, "is": true, "was": true, "there's": true, "it's": true,
}

// reworks reports how a turn says the last attempt did not do the job, and ""
// when it does not.
func reworks(lines []string, subject, prev map[string]bool) string {
	if showsFailure(lines) {
		return Pasted
	}
	text := strings.Join(lines, " ")
	low := strings.ToLower(strings.ReplaceAll(text, "’", "'"))
	if len(low) > opening {
		low = low[:opening]
	}
	if persists.MatchString(stillFine.ReplaceAllString(low, " ")) || plainly.MatchString(low) ||
		negated.MatchString(low) || expected.MatchString(low) || wentWrongIn(low) {
		return Said
	}
	// "try again", "deploy again": a bare redo. A longer turn with "again" in
	// it is the same thing asked for with a difference — "run it again with
	// verbose logging" — unless it says what went wrong, which is seen above.
	if words := strings.Fields(low); len(words) <= 4 && strings.Contains(" "+strings.Join(words, " ")+" ", " again ") {
		return Again
	}
	if len(subject) >= 2 && len(prev) >= 2 && textmatch.Overlap(subject, prev) >= sameRequest {
		return Again
	}
	return ""
}

// showsFailure reports whether any of lines is a program saying something
// went wrong.
func showsFailure(lines []string) bool {
	for _, l := range lines {
		if output.MatchString(l) {
			return true
		}
	}
	return false
}

// wentWrongIn reports whether low names something having gone wrong, as a
// thing that happened and not as a thing to build or to ask about.
func wentWrongIn(low string) bool {
	for _, c := range clause.Split(low, -1) {
		words := wordLike.FindAllString(c, -1)
	next:
		for i, w := range words {
			if !wentWrongs(w) {
				continue
			}
			if i+1 < len(words) && asVocabulary[words[i+1]] {
				continue
			}
			for j := i - 1; j >= 0 && j >= i-5; j-- {
				if toBuild[words[j]] {
					continue next
				}
			}
			// "an error", "a 500 error": any error, unless something put it
			// in front of the person.
			a := i - 1
			if a >= 1 && words[a] != "a" && words[a] != "an" {
				a--
			}
			if a >= 0 && (words[a] == "a" || words[a] == "an") {
				seen := false
				for j := a - 1; j >= 0 && j >= a-3; j-- {
					seen = seen || reported[words[j]]
				}
				if !seen {
					continue
				}
			}
			return true
		}
	}
	return false
}

func wentWrongs(w string) bool { return wentWrong.MatchString(w) }

// sameRequest is how much of the shorter of two consecutive requests the other
// must hold for the second to be the first one sent again.
const sameRequest = 0.8
