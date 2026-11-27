package effect

import (
	"testing"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

func user(text string, ts int64) transcript.Turn {
	return transcript.Turn{Role: "user", Text: text, TS: ts}
}

func tool(name, input string, ts int64) transcript.Turn {
	return transcript.Turn{Role: "tool", Tool: name, Input: input, Status: "ok", TS: ts}
}

func says(text string) transcript.Turn { return transcript.Turn{Role: "assistant", Text: text} }

// whole measures a session as the one sitting it is when nothing breaks it.
func whole(s *transcript.Session) (Start, bool) { return measure(s, 0, len(s.Turns)) }

func sessionOf(turns ...transcript.Turn) *transcript.Session {
	return &transcript.Session{Harness: "claude-code", ID: "abc12345-rest", Project: "shop", Started: 1000, Turns: turns}
}

func TestStartCountsWhatThePersonWroteAndTheAgentLookedUpBeforeTheFirstEdit(t *testing.T) {
	st, ok := whole(sessionOf(
		user("fix the checkout crash", 1000),
		tool("Read", "cart.go", 1010),
		tool("Grep", "", 1020),
		says("Which cart do you mean?"),
		user("the empty one", 1100),
		tool("Bash", "go test ./cart", 1150),
		tool("Edit", "cart.go", 1300),
		// Past the first edit: none of it is the start.
		user("now also rename the package to basket please", 1400),
		tool("Read", "basket.go", 1410),
	))
	if !ok {
		t.Fatal("a session a person wrote in was not measured")
	}
	if st.Asks != 2 || st.Words != 7 || st.Lookups != 3 || !st.Edited {
		t.Errorf("got %d asks, %d words, %d lookups, edited %v; want 2, 7, 3, true", st.Asks, st.Words, st.Lookups, st.Edited)
	}
	if !st.Timed() || st.Minutes() != 5 {
		t.Errorf("first turn at 1000 and first edit at 1300 is 5 minutes, got timed %v, %v", st.Timed(), st.Minutes())
	}
	if st.Restore != Cold {
		t.Errorf("nothing was handed over, got %q", st.Restore)
	}
}

func TestWhatTheHostWroteIsNotThePersonWriting(t *testing.T) {
	st, ok := whole(sessionOf(
		user("<system-reminder>forty words the host injected about hooks and tools and memory</system-reminder>", 1000),
		user("<command-name>/clear</command-name>", 1001),
		transcript.Turn{Role: "host", Text: "This session is being continued from a previous conversation", TS: 1002},
		user("add a retry <pasted_content id=\"1\">two hundred lines of somebody's log</pasted_content>", 1003),
		tool("Write", "retry.go", 1100),
	))
	if !ok || st.Asks != 1 || st.Words != 3 {
		t.Errorf("got ok %v, %d asks, %d words; the person wrote one turn of three words", ok, st.Asks, st.Words)
	}
	if st.AskedAt != 1003 {
		t.Errorf("the clock starts at the person's first turn, 1003, not %d", st.AskedAt)
	}
}

func TestASessionThatNeverEditedIsCountedToItsEndAndHasNoTime(t *testing.T) {
	st, ok := whole(sessionOf(
		user("what does the retry policy do", 1000),
		tool("Read", "retry.go", 1010),
		says("It backs off."),
	))
	if !ok || st.Edited || st.Timed() || st.Lookups != 1 {
		t.Errorf("got ok %v edited %v timed %v lookups %d", ok, st.Edited, st.Timed(), st.Lookups)
	}
}

func TestASessionNobodyWroteInIsNotMeasured(t *testing.T) {
	if _, ok := whole(sessionOf(
		transcript.Turn{Role: "host", Text: "hook feedback"},
		tool("Edit", "a.go", 5),
	)); ok {
		t.Error("a session with no turn of a person's own was measured as one they started")
	}
}

func TestAnEditBeforeThePersonFirstWroteDoesNotDropTheSitting(t *testing.T) {
	s := sessionOf(
		tool("Write", "scaffold.go", 990),
		user("fix the cart", 1000),
		tool("Read", "cart.go", 1010),
		tool("Edit", "cart.go", 1060),
	)
	st, ok := whole(s)
	if !ok || !st.Edited || st.EditAt != 1060 || st.Lookups != 1 {
		t.Errorf("got ok %v, edited %v at %d after %d lookups; the edit before the person wrote is not theirs, so the first is at 1060 after 1", ok, st.Edited, st.EditAt, st.Lookups)
	}
	if got := Sittings(s, Break); len(got) != 1 {
		t.Errorf("got %d sittings, want the one the person sat down to", len(got))
	}
}

func TestAFailedEditIsNotTheFirstEdit(t *testing.T) {
	failed := tool("Edit", "cart.go", 1010)
	failed.Status = "error"
	st, _ := whole(sessionOf(user("fix the cart", 1000), failed, tool("Edit", "cart.go", 1060)))
	if !st.Edited || st.EditAt != 1060 || st.Lookups != 1 {
		t.Errorf("edit at %d after %d lookups; the edit that failed changed nothing, so the first is at 1060 after 1", st.EditAt, st.Lookups)
	}
}

func TestResumeIsReadFromItsAnswerAndIsNotALookup(t *testing.T) {
	resume := func(status, text string) transcript.Turn {
		return transcript.Turn{Role: "tool", Tool: "mcp__plugin_logos_logos__resume", Status: status, Text: text}
	}
	for _, tc := range []struct {
		name string
		call transcript.Turn
		want string
	}{
		{"a checkpoint came back", resume("ok", "## Where the last agent stopped\nwaiting on a quote"), Restored},
		{"it said there was none", resume("ok", "notes\n_"+session.NoCheckpointYet+" — this is context, not a handoff._"), Cold},
		{"the call failed", resume("error", "vault not found"), Cold},
		{"the host kept no answer", resume("ok", ""), Unclear},
	} {
		st, _ := whole(sessionOf(user("carry on with the quote", 1000), tc.call, tool("Edit", "quote.md", 1100)))
		if st.Restore != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, st.Restore, tc.want)
		}
		if st.Lookups != 0 {
			t.Errorf("%s: the resume was counted as a lookup", tc.name)
		}
	}

	other := transcript.Turn{Role: "tool", Tool: "mcp__github__resume", Status: "ok", Text: "resumed"}
	if st, _ := whole(sessionOf(user("go on", 1), other, tool("Edit", "a.go", 2))); st.Restore != Cold || st.Lookups != 1 {
		t.Errorf("another server's resume was taken for Logos's: %+v", st)
	}

	late := resume("ok", "## Where the last agent stopped")
	if st, _ := whole(sessionOf(user("go on", 1), tool("Edit", "a.go", 2), late)); st.Restore != Cold {
		t.Error("a resume after the first edit did not shape how the session began, and was counted as if it had")
	}

	if st, _ := whole(sessionOf(user("go on", 1), resume("ok", "## Where the last agent stopped"),
		resume("ok", session.NoCheckpointYet), tool("Edit", "a.go", 2))); st.Restore != Restored {
		t.Error("a second resume that found nothing took back the checkpoint the first handed over")
	}
}

func TestAShellCommandThatWritesAFileIsAnEdit(t *testing.T) {
	for cmd, want := range map[string]bool{
		"cat > internal/a.go <<'EOF'\npackage a\nif x > 3 {}\nEOF": true,
		"cd repo && sed -i '' 's/a/b/' main.go":                    true,
		"sed --in-place s/a/b/ main.go":                            true,
		"perl -pi -e 's/a/b/' main.go":                             true,
		"echo done >> notes.md":                                    true,
		"go test ./... | tee out.log":                              true,
		"git apply fix.patch":                                      true,
		"apply_patch <<'EOF'\n*** Begin Patch\nEOF":                true,
		"echo \"x\" > \"my file.txt\"":                             true,

		"go test ./... 2>/dev/null":                            false,
		"go build ./... > /dev/null 2>&1":                      false,
		"echo oops >&2":                                        false,
		"awk '$1 > 3' data.csv":                                false,
		"python3 -c \"print(1 > 0)\"":                          false,
		"python3 - <<'EOF'\nopen('x','w').write('a > b')\nEOF": false,
		"git log --format=%an->%s":                             false,
		"sed -n 1,20p main.go":                                 false,
		"grep -rn steel .":                                     false,
		"":                                                     false,
	} {
		if got := shellWrites(cmd); got != want {
			t.Errorf("shellWrites(%q) = %v, want %v", cmd, got, want)
		}
	}
	st, _ := whole(sessionOf(user("fix it", 1), tool("Bash", "grep -rn cart .", 2), tool("Bash", "sed -i '' s/a/b/ cart.go", 3)))
	if !st.Edited || st.Lookups != 1 {
		t.Errorf("an edit made from the shell was not the first edit: %+v", st)
	}
}

func edited(restore string, asks, words, lookups int, minutes int64) Start {
	return Start{Restore: restore, Edited: true, Asks: asks, Words: words, Lookups: lookups, AskedAt: 1000, EditAt: 1000 + minutes*60}
}

func TestCompareTakesMediansOverTheSessionsThatEdited(t *testing.T) {
	c := Compare([]Start{
		edited(Restored, 1, 10, 4, 2),
		edited(Restored, 1, 12, 6, 4),
		edited(Restored, 9, 900, 80, 60), // the outlier a mean would follow
		{Restore: Restored},              // never edited: counted, not in the medians
		edited(Cold, 2, 40, 10, 8),
		edited(Cold, 4, 60, 20, 12),
		{Restore: Unclear, Edited: true, Asks: 1},
	})
	if g := c.Restored; g.Sittings != 4 || g.Edited != 3 || g.Asks != 1 || g.Words != 12 || g.Lookups != 6 || g.Minutes != 4 || g.Timed != 3 {
		t.Errorf("restored %+v", g)
	}
	if g := c.Cold; g.Sittings != 2 || g.Edited != 2 || g.Asks != 3 || g.Words != 50 || g.Lookups != 15 || g.Minutes != 10 {
		t.Errorf("cold %+v", g)
	}
	if c.Unclear != 1 {
		t.Errorf("unclear = %d; a resume with no answer kept belongs to neither arm", c.Unclear)
	}
	if c.Comparable() {
		t.Error("three sessions against two were called comparable")
	}
}

func TestComparableNeedsEnoughEditedSessionsOnBothSides(t *testing.T) {
	var starts []Start
	for i := 0; i < Enough; i++ {
		starts = append(starts, edited(Restored, 1, 10, 4, 2), edited(Cold, 2, 40, 10, 8))
	}
	if !Compare(starts).Comparable() {
		t.Errorf("%d edited sessions a side is the bar, and it was not met", Enough)
	}
	// One side padded with sessions that never edited is still one short.
	short := append(starts[:len(starts)-1:len(starts)-1], Start{Restore: Cold})
	if Compare(short).Comparable() {
		t.Error("a session that never edited was counted toward the bar")
	}
}

func TestASessionWithNoTimesIsLeftOutOfTheMinutesOnly(t *testing.T) {
	untimed := Start{Restore: Cold, Edited: true, Asks: 3, Words: 30, Lookups: 9}
	g := Compare([]Start{untimed, edited(Cold, 1, 10, 3, 6)}).Cold
	if g.Edited != 2 || g.Timed != 1 || g.Minutes != 6 || g.Asks != 2 {
		t.Errorf("%+v: both count toward asks, only the timed one toward minutes", g)
	}
}

// A session left open and come back to is several sittings, and each began in
// its own way: what it took to reach the first edit is counted from where the
// person came back, not from the session's first line.
func TestASessionComeBackToAfterABreakIsSeveralSittings(t *testing.T) {
	hour := int64(3600)
	s := sessionOf(
		user("fix the cart total", 1000),
		tool("Read", "cart.go", 1010),
		tool("Edit", "cart.go", 1100),
		says("Fixed."),
		// Twenty minutes: a pause, and the same sitting.
		user("and the tax line", 1100+20*60),
		tool("Edit", "tax.go", 1100+21*60),
		// The next day.
		user("now the receipts page please", 1000+24*hour),
		tool("Grep", "receipt", 1000+24*hour+30),
		tool("Read", "receipt.go", 1000+24*hour+40),
		tool("Edit", "receipt.go", 1000+24*hour+120),
		// A week on, a question and nothing edited.
		user("what did we decide about rounding", 1000+200*hour),
		says("Half up."),
	)
	got := Sittings(s, Break)
	if len(got) != 3 {
		t.Fatalf("want 3 sittings, got %d: %+v", len(got), got)
	}
	first, second, third := got[0], got[1], got[2]
	if first.Sitting != 0 || first.After != 0 || first.Began != 1000 || first.Asks != 1 || first.Lookups != 1 || !first.Edited {
		t.Errorf("the sitting that opens the session: %+v", first)
	}
	if first.First != 0 || first.End != 6 || second.First != 6 || second.End != 10 || third.First != 10 || third.End != 12 {
		t.Errorf("the sittings do not cover the session's turns end to end: %+v", got)
	}
	if second.Sitting != 1 || second.After != 1100+21*60 || second.AskedAt != 1000+24*hour || second.Began != second.AskedAt {
		t.Errorf("the second sitting begins where they came back: %+v", second)
	}
	if second.Asks != 1 || second.Words != 5 || second.Lookups != 2 || !second.Edited || second.Minutes() != 2 {
		t.Errorf("the second sitting is measured from its own first turn: %+v", second)
	}
	if third.Sitting != 2 || third.Edited || third.Asks != 1 {
		t.Errorf("a sitting that edited nothing is still a sitting: %+v", third)
	}
	if one, _ := whole(s); len(Sittings(s, 0)) != 1 || Sittings(s, 0)[0].Asks != one.Asks {
		t.Error("with no break to look for, a session is one sitting")
	}
}

// The agent at work is not a break, however long it takes; the gap is from
// the last thing anyone wrote to the person's next turn.
func TestALongTaskIsNotABreak(t *testing.T) {
	s := sessionOf(
		user("run the whole benchmark", 1000),
		tool("Bash", "make bench", 1000+50*60), // came back fifty minutes later
		says("Done: 84%."),
		user("and commit the result", 1000+52*60),
		tool("Edit", "RESULTS.md", 1000+53*60),
	)
	if got := Sittings(s, Break); len(got) != 1 || got[0].Asks != 2 {
		t.Errorf("want one sitting with both asks in it, got %+v", got)
	}
}

// What the host writes after a break — a hook's context, a summary — is not
// the person coming back, and does not hide that they then did.
func TestTheHostsTextDoesNotStartOrHideASitting(t *testing.T) {
	day := int64(86400)
	s := sessionOf(
		user("fix the cart", 1000),
		tool("Edit", "cart.go", 1060),
		transcript.Turn{Role: "host", Text: "This session is being continued from a previous conversation", TS: 1000 + day},
		transcript.Turn{Role: "user", Text: "<system-reminder>SessionStart hook ran</system-reminder>", TS: 1000 + day + 1},
		user("carry on with the receipts", 1000+day+30),
	)
	got := Sittings(s, Break)
	if len(got) != 2 || got[1].First != 4 || got[1].After != 1060 {
		t.Errorf("want the second sitting to begin at the person's own turn, after the last edit: %+v", got)
	}
}

// A host with no time on a turn cannot be split: its chat is one sitting.
func TestASessionWithNoTimesIsOneSitting(t *testing.T) {
	s := sessionOf(user("fix the cart", 0), tool("Edit", "cart.go", 0), user("now the tax", 0), tool("Edit", "tax.go", 0))
	if got := Sittings(s, Break); len(got) != 1 {
		t.Errorf("want 1 sitting, got %d", len(got))
	}
}

func TestBySittingComparesOpenersWithOpenersAndTheRestWithTheRest(t *testing.T) {
	starts := []Start{
		{Sitting: 0, Restore: Restored}, {Sitting: 0, Restore: Cold}, {Sitting: 0, Restore: Cold},
		{Sitting: 1, Restore: Restored}, {Sitting: 4, Restore: Restored}, {Sitting: 2, Restore: Cold}, {Sitting: 1, Restore: Unclear},
	}
	opened, pickedUp := BySitting(starts)
	if opened.Restored.Sittings != 1 || opened.Cold.Sittings != 2 || opened.Unclear != 0 {
		t.Errorf("opened: %+v", opened)
	}
	if pickedUp.Restored.Sittings != 2 || pickedUp.Cold.Sittings != 1 || pickedUp.Unclear != 1 {
		t.Errorf("picked up: %+v", pickedUp)
	}
}
