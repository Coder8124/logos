package effect

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/textmatch"
	"github.com/Coder8124/logos/internal/transcript"
)

// howSaid is reworks for a turn with nothing before it to be a repeat of.
func howSaid(text string) string {
	return reworks(strings.Split(text, "\n"), textmatch.Subject(text), nil)
}

// These were written before the lists they test were narrowed, from how people
// talk to a coding agent in general and not from any one person's transcripts:
// the lists were first tuned on one machine's turns, where every mention of an
// error was a report of one, and a list that only knows one person is wrong
// about the next.
func TestATurnThatSaysItDidNotWorkIsRework(t *testing.T) {
	for _, text := range []string{
		"that didn't work, same error as before",
		"it's still failing on the second test",
		"nope, still broken",
		"the build is failing again",
		"this doesn't compile",
		"now the login page is blank",
		"it crashes when I click save",
		"I'm getting a 500 error on /api/users",
		"nothing happens when I run it",
		"the output is wrong, it should be 42 but I get 41",
		"doesn't work",
		"it's not picking up the config",
		"hmm, the page won't load",
		"I still see the old header after the deploy",
		"the tests are still red",
		"it was supposed to write a csv but it wrote json",
		"can't connect to the database now",
		"I ran it and it hangs forever",
		"fix the import error",
		"why is it still timing out?",
		"that did not help",
		"try again",
		"the same problem",
		"unable to log in after your change",
		"it isn't showing the new column",
	} {
		if howSaid(text) == "" {
			t.Errorf("not read as rework: %q", text)
		}
	}
}

func TestARequestThatOnlyMentionsFailureIsNotRework(t *testing.T) {
	for _, text := range []string{
		"add error handling to the upload function",
		"now write a test for the failure case",
		"log an error when the token is missing",
		"can you also make it retry on failure",
		"don't deploy yet, run the tests first",
		"do not run the migration until I say so",
		"why not use a vector here?",
		"make sure it works without network access",
		"rename the error variable to err",
		"what does this error class do?",
		"run it again with verbose logging turned on",
		"should we use postgres but keep sqlite for the tests?",
		"great, that works. now add pagination",
		"thanks, I still need the docs updated though",
		"do you still have the earlier plan?",
		"commit and push",
		"is the fix deployed?",
		"handle the case where the user is not found",
		"return an error if the file is missing",
		"show an error page for a 404",
		"what is the difference between a crash and a hang?",
		"never commit the .env file",
		"add a timeout to the http client",
		"yes",
		"explain how the retry logic works",
	} {
		if how := howSaid(text); how != "" {
			t.Errorf("read as rework (%s): %q", how, text)
		}
	}
}

func TestAnErrorPastedBackIsRework(t *testing.T) {
	for _, text := range []string{
		"Traceback (most recent call last):\n  File \"app.py\", line 3, in <module>\n    import flask\nModuleNotFoundError: No module named 'flask'",
		"ERROR: Could not find a version that satisfies the requirement torch==2.1.1",
		"$ npm start\nsh: react-scripts: command not found",
		"INFO:     127.0.0.1:55536 - \"POST /api/v1/jobs HTTP/1.1\" 422 Unprocessable Entity",
		"{\"event\": \"job failed\", \"level\": \"error\", \"timestamp\": \"2025-07-30T06:27:51Z\"}",
		"src/main.go:41:2: undefined: parseFlags",
		"TypeError: Cannot read properties of undefined (reading 'map')",
		"Job processing failed: no workers available",
	} {
		if how := howSaid(text); how != Pasted {
			t.Errorf("want pasted, got %q for %q", how, text)
		}
	}
	// Output that reports nothing wrong is something shown, not something failing.
	for _, text := range []string{
		"$ kubectl get pods\nNAME        READY   STATUS    RESTARTS\napi-7c84    1/1     Running   0",
		"here is the plan:\n1. build the image\n2. push it",
	} {
		if how := howSaid(text); how != "" {
			t.Errorf("read as rework (%s): %q", how, text)
		}
	}
}

func you(text string) transcript.Turn { return transcript.Turn{Role: "user", Text: text} }

// reworkIn is the replies of a session that were rework.
func reworkIn(replies []Reply) []Reply {
	var out []Reply
	for _, r := range replies {
		if r.Rework != "" {
			out = append(out, r)
		}
	}
	return out
}

func TestTheSameRequestSentAgainIsRework(t *testing.T) {
	replies := Replies(sessionOf(
		you("stop the dev server on port 3000"),
		says("Both servers are stopped."),
		you("stop the dev server running on port 3000"),
		says("Done."),
		you("now open the pull request"),
	))
	if len(replies) != 2 {
		t.Errorf("want 2 turns after the agent acted, got %d", len(replies))
	}
	if found := reworkIn(replies); len(found) != 1 || found[0].Rework != Again || found[0].Turn != 2 {
		t.Errorf("want the second request found as sent again, got %+v", found)
	}
}

// A request the person stopped themselves and sent again is their doing.
func TestARequestCutShortAndSentAgainIsNotRework(t *testing.T) {
	replies := Replies(sessionOf(
		you("find where the cart total is rounded"),
		says("Searching."),
		you("[Request interrupted by user for tool use]"),
		you("find where the cart total is rounded"),
	))
	if found := reworkIn(replies); len(found) != 0 {
		t.Errorf("an interrupted request sent again was read as rework: %+v", found)
	}
}

// The first thing a person says is the request, however it is worded: there is
// nothing yet for it to be coming back from.
func TestTheFirstTurnIsNeverRework(t *testing.T) {
	if replies := Replies(sessionOf(
		you("the build is still failing with the same error, fix it"),
		says("Looking."),
	)); len(replies) != 0 {
		t.Errorf("want nothing counted before the agent acted, got %+v", replies)
	}
}

func TestHostTextIsNotAReply(t *testing.T) {
	if replies := Replies(sessionOf(
		you("deploy it"),
		says("Deployed."),
		you("<bash-input>kubectl logs api</bash-input>"),
		you("<bash-stdout>ERROR: connection refused</bash-stdout><bash-stderr></bash-stderr>"),
		you("<system-reminder>the build failed again</system-reminder>"),
	)); len(replies) != 0 {
		t.Errorf("the host's text was counted as the person's: %+v", replies)
	}
}

// Where the host marks a paste, the person's own words do not hold it; an
// error pasted back is still the turn, with words beside it or none.
func TestAnErrorInAMarkedPasteIsRework(t *testing.T) {
	replies := Replies(sessionOf(
		you("deploy it"),
		says("Deployed."),
		you("<pasted_content id=\"1\">$ curl localhost:8000/health\ncurl: (7) Failed to connect\nERROR: connection refused</pasted_content>"),
		says("Restarted the service."),
		you("here is the plan we agreed <pasted_content id=\"2\">1. build\n2. push</pasted_content> carry on"),
	))
	if len(replies) != 2 {
		t.Errorf("want both turns counted, the one that is only a paste too, got %d", len(replies))
	}
	if found := reworkIn(replies); len(found) != 1 || found[0].Rework != Pasted || found[0].Turn != 2 {
		t.Errorf("want the pasted error found and the pasted plan left alone, got %+v", found)
	}
}

func TestReworkedCountsAndWithholdsARateItCannotSupport(t *testing.T) {
	var r Reworked
	r.Add([]Reply{{Rework: Said}, {}, {Rework: Pasted}, {Rework: Said}})
	r.Add([]Reply{{}, {}})
	r.Add(nil) // a session with no reply in it is not one that had no rework
	if r.Sessions != 2 || r.WithRework != 1 || r.Replies != 6 || r.Rework != 3 || r.By[Said] != 2 || r.By[Pasted] != 1 {
		t.Errorf("counted %+v", r)
	}
	if _, ok := r.Rate(); ok {
		t.Error("three of six was given as a rate")
	}
	r.Add([]Reply{{}, {}, {}, {Rework: Again}})
	if rate, ok := r.Rate(); !ok || rate != 0.4 {
		t.Errorf("want 4 of 10 as 0.4, got %v %v", rate, ok)
	}
}
