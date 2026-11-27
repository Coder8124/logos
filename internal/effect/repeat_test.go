package effect

import (
	"testing"

	"github.com/Coder8124/logos/internal/transcript"
)

func TestATurnThatOpensByCallingTheAgentBackIsACorrection(t *testing.T) {
	for text, want := range map[string]bool{
		"no, install it through homebrew and not from source": true,
		"Don’t commit the generated files":                    true,
		"stop editing the vendor directory":                   true,
		"stop, the vendor directory is generated":             true,
		"I said the retail price is locked":                   true,
		"that's not the staging database":                     true,
		"why did you rewrite the whole module":                true,
		"you forgot the migration for the orders table":       true,
		"never push to main without asking":                   true,
		"not that file, the importer under internal":          true,

		"no":   false, // names nothing
		"stop": false,
		// "no" is not "now", nor "not" "note".
		"now add a retry to the upload client":         false,
		"note the new timeout in the changelog":        false,
		"notice how the cache warms on startup":        false,
		"stopwatch the upload path and report timings": false,
		// Openings that start like a correction and are not one.
		"no problem, carry on with the importer":     false,
		"not sure which importer you mean":           false,
		"never mind the importer, look at the feed":  false,
		"don't you need the schema for the importer": false,
		// What the first real transcripts said, and were wrongly counted for:
		// a request to stop something, and a question with "instead of" in it.
		"stop the service running on port 8000 and 3000":                          false,
		"stop all the services running on port 8000, 3000 on my mac":              false,
		"why does the script say brain@latest instead of logos@latest ?":          false,
		"use the formula rather than the source build for the remaining machines": false,

		"add pagination to the orders endpoint": false,
		"what does the retry policy do":         false,
		"thanks, that works":                    false,
	} {
		if got := corrects(text); got != want {
			t.Errorf("corrects(%q) = %v, want %v", text, got, want)
		}
	}

	long := "Please build the importer for the orders feed. It should stream rows, validate each against the schema, " +
		"write rejects to a side file and report counts at the end, with a dry-run flag for the first few runs against production data. " +
		"Also don't do anything to the legacy importer."
	if corrects(long) {
		t.Error("a long request with a condition in its last sentence was read as a correction")
	}
}

var brew = Known{Ref: "memory 12", Text: "Logos is installed through Homebrew, never built from source", Since: 500}

func correcting(text string) *transcript.Session {
	return sessionOf(
		user("upgrade logos", 1000),
		tool("Bash", "go install ./cmd/logos", 1010),
		user(text, 1020),
	)
}

func TestACorrectionAboutSomethingHeldIsARepeat(t *testing.T) {
	cs := corrections(correcting("no, use homebrew, not a source build"), Hold([]Known{brew}, nil))
	if len(cs) != 1 || cs[0].Known == nil || cs[0].Known.Ref != "memory 12" {
		t.Fatalf("got %+v, want one correction restating memory 12", cs)
	}
	if cs[0].At != 1020 || cs[0].Session != "abc12345-rest" {
		t.Errorf("correction is dated and placed by its own turn, got %+v", cs[0])
	}

	cs = corrections(correcting("no, pin the version in the lockfile instead"), Hold([]Known{brew}, nil))
	if len(cs) != 1 || cs[0].Known != nil {
		t.Errorf("a correction about something new was matched to a memory: %+v", cs)
	}
}

func TestTheFirstRequestIsNeverACorrection(t *testing.T) {
	s := sessionOf(
		user("don't build logos from source, install it through homebrew", 1000),
		user("stop and use homebrew, not source", 1001), // still nothing to call back from
		tool("Bash", "brew install logos", 1010),
	)
	if cs := corrections(s, Hold([]Known{brew}, nil)); len(cs) != 0 {
		t.Errorf("before the agent had done anything there was nothing to correct, got %+v", cs)
	}
}

func TestOnlyWhatWasHeldWhenTheSessionBeganCanBeRepeated(t *testing.T) {
	late := brew
	late.Since = 1015 // stored mid-session, after the agent was already running
	if cs := corrections(correcting("no, use homebrew, not a source build"), Hold([]Known{late}, nil)); cs[0].Known != nil {
		t.Error("a memory stored after the session began was counted as known to it")
	}
	undated := correcting("no, use homebrew, not a source build")
	undated.Started = 0
	for i := range undated.Turns {
		undated.Turns[i].TS = 0 // a host that keeps no time anywhere, as Cursor's
	}
	if cs := corrections(undated, Hold([]Known{brew}, nil)); cs[0].Known != nil {
		t.Error("a session with no start time cannot be said to have begun after anything")
	}
}

func TestOneProjectsMemoryDoesNotAnswerForAnother(t *testing.T) {
	elsewhere := brew
	elsewhere.Project = "kestrel"
	text := "no, use homebrew, not a source build"
	if cs := corrections(correcting(text), Hold([]Known{elsewhere}, nil)); cs[0].Known != nil {
		t.Error("a memory filed under kestrel matched a correction made in shop")
	}
	here := brew
	here.Project = "shop/worktree-2"
	sameRepo := func(a, b string) bool { return a == "shop/worktree-2" && b == "shop" }
	if cs := corrections(correcting(text), Hold([]Known{here}, sameRepo)); cs[0].Known == nil {
		t.Error("the caller's own rule for one project under two names was not used")
	}

	// Cursor names no project: only what applies everywhere may match.
	nowhere := correcting(text)
	nowhere.Project = ""
	if cs := corrections(nowhere, Hold([]Known{here}, sameRepo)); cs[0].Known != nil {
		t.Error("a session with no project was matched to a project's memory by guessing")
	}
	if cs := corrections(nowhere, Hold([]Known{brew}, nil)); cs[0].Known == nil {
		t.Error("a memory that applies everywhere applies to a session with no project too")
	}
}

func TestOneSharedWordIsNotARepeat(t *testing.T) {
	cookies := Known{Ref: "memory 3", Text: "session cookies expire after a day", Since: 500}
	cs := corrections(correcting("stop capturing session activity in the hook"), Hold([]Known{cookies}, nil))
	if len(cs) != 1 || cs[0].Known != nil {
		t.Errorf("\"session\" alone joined two different subjects: %+v", cs)
	}
}

func TestTheBestMatchIsTheOneNamed(t *testing.T) {
	loose := Known{Ref: "memory 1", Text: "Homebrew formula updates land weekly; source tarballs, release notes, checksums and signatures follow", Since: 100}
	cs := corrections(correcting("no, use homebrew, not a source build"), Hold([]Known{loose, brew}, nil))
	if cs[0].Known == nil || cs[0].Known.Ref != "memory 12" {
		t.Errorf("got %+v, want the memory the correction is most about", cs[0].Known)
	}
}

func TestRepeatsCountsAndWithholdsARateItCannotSupport(t *testing.T) {
	var r Repeats
	r.Add(nil)
	r.Add([]Correction{{At: 30, Known: &brew}, {At: 40}})
	r.Add([]Correction{{At: 10, Known: &brew}})
	if r.Sessions != 3 || r.Corrected != 2 || r.Corrections != 3 || r.Repeated != 2 {
		t.Fatalf("%+v", r)
	}
	if len(r.Each) != 2 || r.Each[0].At != 10 {
		t.Errorf("the repeated ones are listed oldest first, got %+v", r.Each)
	}
	if _, ok := r.Rate(); ok {
		t.Error("two of three was given as a percentage")
	}
	for r.Corrections < EnoughForRate {
		r.Add([]Correction{{At: 50}})
	}
	if rate, ok := r.Rate(); !ok || rate != 0.2 {
		t.Errorf("2 of %d: got %v, %v", r.Corrections, rate, ok)
	}
}

// corrections reads a session the way a report does: by the sittings it breaks
// into.
func corrections(s *transcript.Session, held Held) []Correction {
	return Corrections(s, held, Sittings(s, Break))
}

func TestAMemorySavedBetweenTwoSittingsIsHeldWhenTheLaterOneCorrects(t *testing.T) {
	saved := brew
	saved.Since = 5000 // after the session began, before the person came back
	s := sessionOf(
		user("upgrade logos", 1000),
		tool("Bash", "go install ./cmd/logos", 1010),
		user("thanks", 1020),
		user("upgrade logos again", 9000), // hours later: a second sitting
		tool("Bash", "go install ./cmd/logos", 9010),
		user("no, use homebrew, not a source build", 9020),
	)
	if n := len(Sittings(s, Break)); n != 2 {
		t.Fatalf("the fixture has %d sittings, want 2", n)
	}
	cs := corrections(s, Hold([]Known{saved}, nil))
	if len(cs) != 1 || cs[0].Known == nil {
		t.Errorf("a memory saved before the sitting began was not counted as held by it: %+v", cs)
	}
}

func TestAPasteWithNoWordsBesideItIsTheSameTurnForSittingsAndReplies(t *testing.T) {
	pasted := user(`<pasted_content id="1">panic: nil pointer in cart.go:12</pasted_content>`, 9000)
	s := sessionOf(
		user("fix the cart", 1000),
		tool("Edit", "cart.go", 1010),
		pasted,
		tool("Edit", "cart.go", 9100),
	)
	if len(Replies(s)) != 1 {
		t.Fatal("the fixture's paste was not a reply")
	}
	if n := len(Sittings(s, Break)); n != 2 {
		t.Errorf("got %d sittings; the person came back after a break with a paste, and Replies counted that turn as theirs", n)
	}
}
