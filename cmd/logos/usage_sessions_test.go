package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/effect"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/scope"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// transcriptsAre stands in for the hosts on this machine.
func transcriptsAre(t *testing.T, sessions ...*transcript.Session) {
	t.Helper()
	was := sessionsSince
	sessionsSince = func(time.Time) ([]*transcript.Session, []string) { return sessions, nil }
	t.Cleanup(func() { sessionsSince = was })
}

// began builds a session that reached its first edit after the person wrote
// `words` words and the agent made `lookups` tool calls, `minutes` in.
func began(id string, start int64, words, lookups int, minutes int64) *transcript.Session {
	s := &transcript.Session{Harness: "claude-code", ID: id, Project: "shop", Started: start}
	s.Turns = append(s.Turns, transcript.Turn{Role: "user", Text: strings.TrimSpace(strings.Repeat("word ", words)), TS: start})
	for i := 0; i < lookups; i++ {
		s.Turns = append(s.Turns, transcript.Turn{Role: "tool", Tool: "Read", Input: "cart.go", Status: "ok", TS: start + 1})
	}
	s.Turns = append(s.Turns, transcript.Turn{Role: "tool", Tool: "Edit", Input: "cart.go", Status: "ok", TS: start + minutes*60})
	return s
}

func TestUsageSessionsSetsRestoredStartsBesideColdOnesAndSaysItIsNotAnExperiment(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	day := time.Now().Add(-3 * 24 * time.Hour).Unix()

	var sessions []*transcript.Session
	for i := 0; i < effect.Enough; i++ {
		// Restored by the session-start hook: only the ledger knows, under the
		// short id it files a session by.
		id := fmt.Sprintf("aaaa000%d-1111-2222", i)
		sessions = append(sessions, began(id, day+int64(i)*3600, 10, 4, 2))
		if err := usagepkg.Record(vault, usagepkg.Event{TS: day + int64(i)*3600 - 5, Kind: usagepkg.KindPack, Project: "shop",
			Via: "cli:resume", Reader: "claude-code", From: "cursor", Session: id[:8], Sent: 400, Full: 400}); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, began(fmt.Sprintf("cccc000%d-1111-2222", i), day+int64(i)*3600, 50, 12, 9))
	}
	// Restored over MCP: no session in the ledger, the answer in the transcript.
	mcp := began("bbbb0000-1111-2222", day, 10, 4, 2)
	mcp.Turns = append(mcp.Turns[:1:1], append([]transcript.Turn{{Role: "tool", Tool: "mcp__logos__resume", Status: "ok",
		Text: "## Where the last agent stopped", TS: day + 1}}, mcp.Turns[1:]...)...)
	// A subagent's transcript, which nobody wrote in, and another project's.
	headless := &transcript.Session{Harness: "claude-code", ID: "dddd0000", Project: "shop", Started: day,
		Turns: []transcript.Turn{{Role: "tool", Tool: "Edit", Input: "a.go", Status: "ok"}}}
	other := began("eeee0000-1111-2222", day, 900, 90, 50)
	other.Project = "kestrel"
	transcriptsAre(t, append(sessions, mcp, headless, other)...)

	out := captureStdout(t, func() {
		if err := runUsage([]string{"shop", "--sessions"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"logos stats --usage --sessions — shop, the last 30 days",
		"the transcripts of 11 sessions you wrote in: claude-code 11",
		"how sittings began — what it took to reach the first file edit (11 began in the window, in 11 sessions)",
		"    from a restore              6       6      1      10        4      6       2m",
		"    cold                        5       5      1      50       12      5       9m",
		"opening a session from a restore against cold: 40 fewer words from you and 8 fewer lookups to the first edit, in 2m against 9m",
		"Measured, not caused: nobody assigned these sittings at random",
		"too few to compare for picking one up again",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ESTIMATE") || strings.Contains(out, "words a minute") {
		t.Errorf("the measured report priced something at a typing speed:\n%s", out)
	}
}

// A session-start hook fires again when a session is reopened. A checkpoint
// handed over then is not how the session began.
func TestACheckpointHandedOverWeeksIntoASessionIsNotHowItBegan(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	day := time.Now().Add(-9 * 24 * time.Hour).Unix()
	for id, at := range map[string]int64{
		"aaaa0000": day - 3,         // before the first turn: began from it
		"bbbb0000": day + 30,        // a prompt on the command line beat the hook's stamp by seconds
		"cccc0000": day + 5*24*3600, // reopened days later
	} {
		if err := usagepkg.Record(vault, usagepkg.Event{TS: at, Kind: usagepkg.KindPack, Project: "shop",
			Via: "cli:resume", Reader: "claude-code", From: "claude-code", Session: id}); err != nil {
			t.Fatal(err)
		}
	}
	never := began("cccc0000-1111-2222", day, 20, 6, 4)
	never.Turns = never.Turns[:len(never.Turns)-1] // and it never edited a file, which once made any restore count
	transcriptsAre(t, began("aaaa0000-1111-2222", day, 10, 4, 2), began("bbbb0000-1111-2222", day, 10, 4, 2), never)

	out := captureStdout(t, func() {
		if err := runUsage([]string{"--sessions"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"    from a restore              2       2",
		"    cold                        1       0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// A session left open is come back to many times, and each time is a sitting
// that began in its own way: reopened and handed a checkpoint, or picked up in
// a window that had stayed open. A checkpoint handed over in the middle of a
// sitting, when the host compacted, began nothing.
func TestUsageSessionsCountsEachTimeASessionWasPickedUpAgain(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	day := time.Now().Add(-9 * 24 * time.Hour).Unix()
	const hour = 3600

	s := began("aaaa0000-1111-2222", day, 10, 4, 2)
	sit := func(at int64, ask string, lookups int) {
		s.Turns = append(s.Turns, transcript.Turn{Role: "user", Text: ask, TS: at})
		for i := 0; i < lookups; i++ {
			s.Turns = append(s.Turns, transcript.Turn{Role: "tool", Tool: "Read", Input: "cart.go", Status: "ok", TS: at + 10})
		}
		s.Turns = append(s.Turns, transcript.Turn{Role: "tool", Tool: "Edit", Input: "cart.go", Status: "ok", TS: at + 180})
	}
	sit(day+24*hour, "carry on with the tax line", 1)          // reopened: the hook handed a checkpoint over
	sit(day+48*hour, "now do the receipts page as well", 6)    // the window had stayed open
	sit(day+48*hour+600, "and the footer", 0)                  // ten minutes on: the same sitting
	sit(day+72*hour, "one more thing, the currency symbol", 5) // open again, and compacted halfway through
	for _, at := range []int64{day + 24*hour - 40, day + 72*hour + 100} {
		if err := usagepkg.Record(vault, usagepkg.Event{TS: at, Kind: usagepkg.KindPack, Project: "shop",
			Via: "cli:resume", Reader: "claude-code", From: "claude-code", Session: "aaaa0000"}); err != nil {
			t.Fatal(err)
		}
	}
	transcriptsAre(t, s)

	out := captureStdout(t, func() {
		if err := runUsage([]string{"--sessions"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"(4 began in the window, in 1 session)",
		"a new one begins when you write after 30 minutes",
		"  opening a session\n    from a restore              0       0      -       -        -      0        -\n    cold                        1       1      1      10        4      1       2m",
		"  picking one up again — the host still held the conversation\n    from a restore              1       1      1       6        1      1       3m\n    nothing handed over         2       2      1     6.5      5.5      2       3m",
		"too few to compare in either pair",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// The window is the sittings that began in it, whenever their session did.
func TestUsageSessionsReadsThisMonthsSittingsOfASessionBegunLongAgo(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	old := time.Now().Add(-200 * 24 * time.Hour).Unix()
	s := began("aaaa0000-1111-2222", old, 10, 4, 2)
	recent := time.Now().Add(-2 * 24 * time.Hour).Unix()
	s.Turns = append(s.Turns,
		transcript.Turn{Role: "user", Text: "pick the refund flow back up", TS: recent},
		transcript.Turn{Role: "tool", Tool: "Edit", Input: "refund.go", Status: "ok", TS: recent + 60})
	transcriptsAre(t, s)

	out := captureStdout(t, func() {
		if err := runUsage([]string{"--sessions"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"(1 began in the window, in 1 session)",
		"    cold                        0       0",
		"    nothing handed over         1       1      1       6        0      1       1m",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestUsageSessionsCountsTheTurnsThatCameBackBecauseItDidNotWork(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	day := time.Now().Add(-4 * 24 * time.Hour).Unix()

	// Began from a restore, and came back once.
	restored := began("aaaa0000-1111-2222", day, 6, 2, 3)
	if err := usagepkg.Record(vault, usagepkg.Event{TS: day - 5, Kind: usagepkg.KindPack, Project: "shop",
		Via: "cli:resume", Reader: "claude-code", From: "cursor", Session: "aaaa0000"}); err != nil {
		t.Fatal(err)
	}
	restored.Turns = append(restored.Turns,
		transcript.Turn{Role: "user", Text: "the checkout page is still blank after that", TS: day + 400},
		transcript.Turn{Role: "assistant", Text: "Fixed the template path.", TS: day + 460},
		transcript.Turn{Role: "user", Text: "good, now add the coupon field", TS: day + 500},
	)
	// Began cold, and came back three times in three ways.
	cold := began("cccc0000-1111-2222", day, 9, 3, 4)
	cold.Turns = append(cold.Turns,
		transcript.Turn{Role: "user", Text: "Traceback (most recent call last):\n  File \"cart.py\", line 9\nKeyError: 'sku'", TS: day + 400},
		transcript.Turn{Role: "assistant", Text: "Guarded the lookup.", TS: day + 460},
		transcript.Turn{Role: "user", Text: "restart the worker pods in staging", TS: day + 500},
		transcript.Turn{Role: "assistant", Text: "Restarted.", TS: day + 560},
		transcript.Turn{Role: "user", Text: "restart the worker pods in staging", TS: day + 600},
		transcript.Turn{Role: "assistant", Text: "Restarted again.", TS: day + 660},
		transcript.Turn{Role: "user", Text: "that didn't help, the queue is not draining", TS: day + 700},
		transcript.Turn{Role: "assistant", Text: "Looking at the consumer.", TS: day + 760},
		transcript.Turn{Role: "user", Text: "add error handling to the consumer while you are there", TS: day + 800},
	)
	// A session that asked once and was answered has no turn that could be rework.
	transcriptsAre(t, restored, cold, began("eeee0000-1111-2222", day, 5, 1, 1))

	out := captureStdout(t, func() {
		if err := runUsage([]string{"--sessions"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"rework — turns in which you came back because the last attempt had not done the job",
		"· 4 of the 7 turns you wrote after an agent had acted, in 2 of 2 sessions",
		"2 said so (still, did not help, an error named), 1 pasted an error back, 1 sent the same request again",
		"in sittings that began from a restore: 1 of 2; in the rest: 3 of 5",
		"with no model",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "%") {
		t.Errorf("four of seven was given as a percentage:\n%s", out)
	}
	for _, said := range []string{"checkout page", "KeyError", "worker pods", "queue is not draining"} {
		if strings.Contains(out, said) {
			t.Errorf("a line of the conversation was printed (%q):\n%s", said, out)
		}
	}
}

func TestUsageSessionsPrintsNoDifferenceItCannotSupport(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	day := time.Now().Add(-2 * 24 * time.Hour).Unix()
	unclear := began("ffff0000-1111-2222", day, 5, 1, 1)
	unclear.Harness = "cursor" // keeps no tool results
	unclear.Turns = append(unclear.Turns[:1:1], append([]transcript.Turn{{Role: "tool", Tool: "mcp_logos_resume", Status: "ok"}}, unclear.Turns[1:]...)...)
	transcriptsAre(t, began("aaaa0000-1111-2222", day, 10, 4, 2), began("cccc0000-1111-2222", day, 50, 12, 9), unclear)

	out := captureStdout(t, func() {
		if err := runUsage([]string{"--sessions"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "too few to compare in either pair") || strings.Contains(out, "fewer") {
		t.Errorf("one session a side was compared:\n%s", out)
	}
	if !strings.Contains(out, "1 more in no row: resume was called and the host kept no answer") {
		t.Errorf("the session whose restore is unknown was put in a row or dropped:\n%s", out)
	}
	if !strings.Contains(out, "claude-code 2, cursor 1") {
		t.Errorf("sessions are not counted per host:\n%s", out)
	}
}

func TestUsageSessionsNamesTheCorrectionsThatRepeatWhatLogosHeld(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	t.Setenv("LOGOS_EMBED", "off")
	day := time.Now().Add(-3 * 24 * time.Hour).Unix()

	ix, err := index.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Store(ix.DB, nil, "", &memory.Memory{
		Text: "Logos is installed through Homebrew, never built from source", Kind: memory.Preference, Created: day - 86400,
	}); err != nil {
		t.Fatal(err)
	}
	// A decision the last checkpoint recorded, and so handed to the next agent.
	if err := session.Commit(ix.DB, vault, &session.Checkpoint{
		Project: "shop", Agent: "cursor", Task: "price the launch", State: "priced", TS: day - 3600,
		Decisions: []string{"retail price is locked at 249 for launch, because the reseller contracts are signed"},
	}); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	s := began("aaaa0000-1111-2222", day, 6, 2, 3)
	s.Turns = append(s.Turns,
		transcript.Turn{Role: "user", Text: "no, use homebrew, not a source build", TS: day + 400},
		transcript.Turn{Role: "assistant", Text: "Switching.", TS: day + 410},
		transcript.Turn{Role: "user", Text: "I said the retail price is locked for launch", TS: day + 500},
		transcript.Turn{Role: "assistant", Text: "Reverting the price.", TS: day + 510},
		transcript.Turn{Role: "user", Text: "don't rename the checkout package yet", TS: day + 600},
	)
	transcriptsAre(t, s, began("cccc0000-1111-2222", day, 9, 3, 4))

	out := captureStdout(t, func() {
		if err := runUsage([]string{"--sessions"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"3 corrections in 1 of 2 sessions",
		"2 of 3 restate something Logos already held when the session began",
		"memory 1: Logos is installed through Homebrew, never built from source",
		"retail price is locked at 249 for launch",
		"read them before trusting the count",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "%") {
		t.Errorf("two of three was given as a percentage:\n%s", out)
	}
	for _, said := range []string{"not a source build", "I said the retail", "rename the checkout"} {
		if strings.Contains(out, said) {
			t.Errorf("a line of the conversation was printed (%q):\n%s", said, out)
		}
	}
}

func TestUsageSessionsFilesASessionStartedInASubdirectoryUnderItsProject(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	repo := t.TempDir()
	sub := filepath.Join(repo, "services", "cart")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, scope.MarkerFile), []byte("storefront\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	day := time.Now().Add(-24 * time.Hour).Unix()
	s := began("dddd0000-1111-2222", day, 5, 1, 2)
	s.Project, s.Cwd = "cart", sub // what the host records: the folder it ran in
	transcriptsAre(t, s)

	out := captureStdout(t, func() {
		if err := runUsage([]string{"storefront", "--sessions"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "read from the transcripts of 1 session") {
		t.Errorf("a session started under storefront's marker was not storefront's:\n%s", out)
	}
}

func TestUsageSessionsDoesNotCountASessionWhoseSittingsAreAllOlderThanTheWindow(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	now := time.Now()
	transcriptsAre(t,
		began("eeee0000-1111-2222", now.Add(-24*time.Hour).Unix(), 5, 1, 2),
		began("ffff0000-1111-2222", now.Add(-90*24*time.Hour).Unix(), 5, 1, 2),
	)
	out := captureStdout(t, func() {
		if err := runUsage([]string{"--sessions"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "read from the transcripts of 1 session you wrote in: claude-code 1") {
		t.Errorf("a session from three months ago was counted in the last thirty days:\n%s", out)
	}
}

func TestDaysWithoutSessionsIsRefusedNotIgnored(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	err := runUsage([]string{"--month", "--days", "90"})
	if err == nil || !strings.Contains(err.Error(), "--days sets how far back --sessions reads") {
		t.Errorf("--days beside --month was accepted and ignored: %v", err)
	}
}

func TestTheNoSessionHintNamesEveryHostItReads(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	transcriptsAre(t)
	out := captureStdout(t, func() {
		if err := runUsage([]string{"--sessions"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, h := range transcript.Harnesses() {
		if !strings.Contains(out, h) {
			t.Errorf("the hint does not name %q:\n%s", h, out)
		}
	}
}

func TestUsageSessionsWithNoTranscriptsSaysSo(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	transcriptsAre(t)
	out := captureStdout(t, func() {
		if err := runUsage([]string{"shop", "--sessions"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "no session for shop in the last 30 days in the transcripts your agents keep") {
		t.Errorf("an empty month printed:\n%s", out)
	}
}

func TestUsageSessionsLooksAsFarBackAsItIsTold(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	var asked time.Time
	was := sessionsSince
	sessionsSince = func(since time.Time) ([]*transcript.Session, []string) { asked = since; return nil, nil }
	t.Cleanup(func() { sessionsSince = was })

	out := captureStdout(t, func() {
		if err := runUsage([]string{"--sessions", "--days", "90"}); err != nil {
			t.Fatal(err)
		}
	})
	asked90 := asked // the 500-day run below asks again
	if year := fmt.Sprint(time.Now().AddDate(0, 0, -499).Year()); !strings.Contains(captureStdout(t, func() {
		if err := runUsage([]string{"--sessions", "--days", "500"}); err != nil {
			t.Fatal(err)
		}
	}), year+" – ") {
		t.Errorf("a window reaching into %s does not say so", year)
	}
	if !strings.Contains(out, "the last 90 days") {
		t.Errorf("the report does not say how far back it read:\n%s", out)
	}
	if back := time.Since(asked90).Hours() / 24; back < 89 || back > 90 {
		t.Errorf("read %.1f days back, want the 90 asked for, today included", back)
	}
	for _, bad := range []string{"0", "-7", "all", ""} {
		if err := runUsage([]string{"--sessions", "--days", bad}); err == nil || !strings.Contains(err.Error(), "--days needs how many days back") {
			t.Errorf("--days %q: got %v, want a plain refusal", bad, err)
		}
	}
}

func TestADifferenceIsWordedAsFewerOrMore(t *testing.T) {
	for _, tc := range []struct {
		restored, cold float64
		want           string
	}{
		{10, 50, "40 fewer words from you"},
		{50, 10, "40 more words from you"},
		{11, 10, "1 more word from you"},
		{7, 7, "the same words from you"},
		{4.5, 6, "1.5 fewer words from you"},
	} {
		if got := versus(tc.restored, tc.cold, "word", "from you"); got != tc.want {
			t.Errorf("versus(%v, %v) = %q, want %q", tc.restored, tc.cold, got, tc.want)
		}
	}
}

func TestUsageSessionsCountsARepeatOfAMemoryThatACorrectionHasSinceReplaced(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	t.Setenv("LOGOS_EMBED", "off")
	day := time.Now().Add(-3 * 24 * time.Hour).Unix()

	ix, err := index.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	old := memory.Memory{
		Text: "Logos is installed through Homebrew, never built from source", Kind: memory.Preference, Created: day - 86400,
	}
	if _, err := memory.Store(ix.DB, nil, "", &old); err != nil {
		t.Fatal(err)
	}
	// Today's correction replaces the memory the session below was handed.
	if _, err := memory.Correct(ix.DB, nil, "", old.ID, &memory.Memory{
		Text: "Logos is installed through Homebrew or npm, never built from source",
	}); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	s := began("aaaa0000-1111-2222", day, 6, 2, 3)
	s.Turns = append(s.Turns,
		transcript.Turn{Role: "user", Text: "no, use homebrew, not a source build", TS: day + 400},
	)
	transcriptsAre(t, s)

	out := captureStdout(t, func() {
		if err := runUsage([]string{"--sessions"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "1 of 1 restate something Logos already held when the session began") {
		t.Errorf("a repeat of a memory since replaced was not counted as held:\n%s", out)
	}
}
