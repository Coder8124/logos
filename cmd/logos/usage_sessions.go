package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/activity"
	"github.com/Coder8124/logos/internal/effect"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/text"
	"github.com/Coder8124/logos/internal/transcript"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// usageSessions prints what the last month's sessions show a restore did, read
// from the transcripts the hosts keep: what it took a sitting to reach its
// first file edit when it began from a checkpoint and when it did not, how
// often the person came back because the last attempt had not done the job,
// and how often they had to call an agent back to something Logos already
// held.
//
// It is the measured answer to the question --month can only estimate. Nothing
// is priced at a typing speed here: words are words the person wrote, lookups
// are tool calls the agent made, minutes are on the host's own clock.
//
// No grant is asked for, on the rule `logos import --from transcripts
// --dry-run` already follows: it reads transcripts and writes nothing to the
// vault. What is printed is counts, and for a repeated correction the vault's
// own record it matched — never a line of a conversation.
//
//	logos usage [project] --sessions [--days N]
func usageSessions(v, project string, days int) error {
	now := time.Now()
	first := now.AddDate(0, 0, -(days - 1))
	from := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, first.Location())

	events, bad, err := usagepkg.Read(v)
	if err != nil {
		return err
	}
	handed := usagepkg.HandedOver(events)
	held, heldNote := heldBy(v)

	// Every project's transcripts are read and the filter applied after: a
	// transcript is filed by the directory it ran in, a ledger by the name the
	// server gave that directory, and SameProject is where the two are joined.
	sessions, problems := sessionsSince(from)

	var starts []effect.Start
	var repeats effect.Repeats
	var rework, reworkRestored effect.Reworked
	perHarness := map[string]int{}
	began := map[string]bool{} // the sessions with a sitting in the window
	for _, s := range sessions {
		if project != "" && !usagepkg.SameProject(s.Project, project) {
			continue
		}
		sittings := effect.Sittings(s, effect.Break)
		if len(sittings) == 0 {
			continue // nobody wrote in it: a subagent's transcript, a headless run
		}
		perHarness[s.Harness]++
		// A sitting belongs to the window it began in, whenever its session
		// did: a session left open since the spring is read for the sittings,
		// the rework and the corrections of this month, each dated on its own.
		times := handed[activity.ShortID(s.ID)]
		for i := range sittings {
			if handedAtTheStart(sittings[i], times) {
				sittings[i].Restore = effect.Restored
			}
			if sittings[i].Began >= from.Unix() {
				starts = append(starts, sittings[i])
				began[s.Harness+"\x00"+s.ID] = true
			}
		}
		var replies, afterRestore []effect.Reply
		for _, r := range effect.Replies(s) {
			if r.At < from.Unix() {
				continue
			}
			replies = append(replies, r)
			if st := sittingOf(sittings, r.Turn); st != nil && st.Restore == effect.Restored {
				afterRestore = append(afterRestore, r)
			}
		}
		rework.Add(replies)
		reworkRestored.Add(afterRestore)
		var inWindow []effect.Correction
		for _, c := range effect.Corrections(s, held) {
			if c.At >= from.Unix() {
				inWindow = append(inWindow, c)
			}
		}
		repeats.Add(inWindow)
	}

	who := "every project"
	if project != "" {
		who = project
	}
	since := "2 Jan"
	if from.Year() != now.Year() {
		since = "2 Jan 2006" // a window that crosses a new year says which July it means
	}
	fmt.Printf("logos stats --usage --sessions — %s, the last %d days (%s – %s)\n",
		who, days, from.Format(since), now.Format("2 Jan 2006"))
	if repeats.Sessions == 0 {
		fmt.Printf("\n· no session for %s in the last %d days in the transcripts your agents keep\n", who, days)
		fmt.Println("  (read from Claude Code, Codex and Cursor where they are installed; --days N looks further back)")
		printTranscriptProblems(problems)
		return nil
	}
	fmt.Printf("read from the transcripts of %d session%s you wrote in: %s\n",
		repeats.Sessions, pluralS(repeats.Sessions), harnessCounts(perHarness))

	printStarts(starts, len(began))
	printRework(rework, reworkRestored)
	printRepeats(repeats, heldNote)
	printTranscriptProblems(problems)
	printUnreadable(bad, v)
	return nil
}

// startSlack is how long after the person's first turn a hook's resume can be
// stamped and still have been there for it: the hook and a prompt given on the
// command line are written within the same moment, in either order.
const startSlack = 60

// handedAtTheStart reports whether one of a session's hook resumes, at times,
// is how this sitting began: made after the sitting before it went quiet, and
// no later than the person's first turn in this one.
//
// A session-start hook also fires when a session is compacted, in the middle
// of a sitting, and that is nobody beginning anything. And one that fires on
// reopening belongs to the sitting that reopening began and to no other: on
// the machine this was first run on, a session begun before Logos was
// installed was handed a checkpoint two months later and counted as having
// started from one. A sitting with no time to compare is left as its
// transcript reads.
func handedAtTheStart(st effect.Start, times []int64) bool {
	first := st.AskedAt
	if first == 0 {
		first = st.Began
	}
	if first == 0 {
		return false
	}
	for _, at := range times {
		if at > st.After && at <= first+startSlack {
			return true
		}
	}
	return false
}

// sittingOf is the sitting a turn belongs to, and nil for a turn in a stretch
// that was not one.
func sittingOf(sittings []effect.Start, turn int) *effect.Start {
	for i := range sittings {
		if turn >= sittings[i].First && turn < sittings[i].End {
			return &sittings[i]
		}
	}
	return nil
}

// sessionsSince reads every session, on every host installed here, that was
// written at or after since, with a line for each one it could not read. A
// seam: tests need no real host on this machine.
//
// Open sessions are read too, unlike the sweep's listing. The sweep records a
// session once it has ended; this measures how one began and what was said in
// it, and both are settled the moment they happen — on a machine where a
// window stays open for weeks, leaving open sessions out left out all of them.
var sessionsSince = func(since time.Time) ([]*transcript.Session, []string) {
	var out []*transcript.Session
	var problems []string
	for _, h := range transcript.Harnesses() {
		paths, err := transcript.Sessions(h)
		if err != nil {
			// A host that is not installed is the ordinary case, not a problem.
			var skip *transcript.SkipReason
			if !errors.As(err, &skip) {
				problems = append(problems, fmt.Sprintf("%s: %v", h, err))
			}
			continue
		}
		for _, p := range paths {
			file := transcript.SourceFile(p)
			fi, err := os.Stat(file)
			if err != nil {
				continue // gone between the listing and the read
			}
			// A file that is one session's own was last written when the
			// session was, so its age rules it out unread. Cursor keeps every
			// chat in one database, whose age says nothing of any one of
			// them: those are read and judged by their own dates.
			own := file == p
			if own && fi.ModTime().Before(since) {
				continue
			}
			s, err := transcript.ReadFile(h, p)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", p, err))
				continue
			}
			if own && s.Ended == 0 {
				s.Ended = fi.ModTime().Unix()
			}
			if !own && s.Started < since.Unix() && s.Ended < since.Unix() {
				continue
			}
			out = append(out, s)
		}
	}
	return out, problems
}

func printStarts(starts []effect.Start, sessions int) {
	opened, pickedUp := effect.BySitting(starts)
	fmt.Println()
	fmt.Printf("how sittings began — what it took to reach the first file edit (%d began in the window, in %d session%s)\n",
		len(starts), sessions, pluralS(sessions))
	fmt.Printf("  A sitting is a stretch you sat down to: a new one begins when you write after %d minutes in which neither you\n", effect.Break/60)
	fmt.Println("  nor the agent wrote anything. Cursor keeps no time on a turn, so each of its chats is one sitting.")
	fmt.Printf("  %-22s %8s %7s %6s %7s %8s %6s %8s\n", "", "sittings", "edited", "asks", "words", "lookups", "timed", "minutes")
	row := func(label string, g effect.Group) {
		asks, words, lookups, minutes := "-", "-", "-", "-"
		if g.Edited > 0 {
			asks, words, lookups = fmtMedian(g.Asks), fmtMedian(g.Words), fmtMedian(g.Lookups)
		}
		if g.Timed > 0 {
			minutes = fmtMinutes(g.Minutes)
		}
		fmt.Printf("    %-20s %8d %7d %6s %7s %8s %6d %8s\n", label, g.Sittings, g.Edited, asks, words, lookups, g.Timed, minutes)
	}
	fmt.Println("  opening a session")
	row("from a restore", opened.Restored)
	row("cold", opened.Cold)
	fmt.Println("  picking one up again — the host still held the conversation")
	row("from a restore", pickedUp.Restored)
	row("nothing handed over", pickedUp.Cold)
	fmt.Println("  Medians over the sittings that edited a file. asks: the turns you wrote before the edit; words: the words in")
	fmt.Println("  them; lookups: the agent's tool calls before it; minutes: the clock from your first turn to the edit, over the")
	fmt.Println("  timed sittings only — the ones whose host keeps a time on each turn.")
	if n := opened.Unclear + pickedUp.Unclear; n > 0 {
		fmt.Printf("· %d more in no row: resume was called and the host kept no answer, so whether a checkpoint came back is unknown\n", n)
	}
	if opened.Comparable() {
		printDifference("opening a session", "cold", opened)
	}
	if pickedUp.Comparable() {
		printDifference("picking one up again", "nothing handed over", pickedUp)
	}
	if opened.Comparable() || pickedUp.Comparable() {
		fmt.Println("  Measured, not caused: nobody assigned these sittings at random. One that opened from a checkpoint is carrying")
		fmt.Println("  work on where a cold one is starting it, and one handed a checkpoint on being picked up is a session that was")
		fmt.Println("  reopened, where the rest were windows left open. It says what happened beside a restore, not what it did.")
	}
	if !opened.Comparable() || !pickedUp.Comparable() {
		which := "in either pair"
		switch {
		case opened.Comparable():
			which = "for picking one up again"
		case pickedUp.Comparable():
			which = "for opening a session"
		}
		fmt.Printf("· too few to compare %s: a difference is printed once both rows of a pair have %d sittings\n", which, effect.Enough)
		fmt.Println("  that edited a file — below that a median is a handful of anecdotes, and the gap between two is noise with a")
		fmt.Println("  number on it")
	}
}

// printDifference words one pair's medians against each other.
func printDifference(when, other string, c effect.Comparison) {
	fmt.Printf("· %s from a restore against %s: %s and %s to the first edit",
		when, other, versus(c.Restored.Words, c.Cold.Words, "word", "from you"), versus(c.Restored.Lookups, c.Cold.Lookups, "lookup", ""))
	if c.Restored.Timed >= effect.Enough && c.Cold.Timed >= effect.Enough {
		fmt.Printf(", in %s against %s", fmtMinutes(c.Restored.Minutes), fmtMinutes(c.Cold.Minutes))
	}
	fmt.Println()
}

func printRework(r, afterRestore effect.Reworked) {
	fmt.Println()
	fmt.Println("rework — turns in which you came back because the last attempt had not done the job")
	if r.Replies == 0 {
		fmt.Println("· nothing to count: no turn of yours in the window came after an agent had acted")
		return
	}
	share := ""
	if rate, ok := r.Rate(); ok {
		share = fmt.Sprintf(" (%.0f%%)", rate*100)
	}
	fmt.Printf("· %d of the %d turn%s you wrote after an agent had acted%s, in %d of %d session%s\n",
		r.Rework, r.Replies, pluralS(r.Replies), share, r.WithRework, r.Sessions, pluralS(r.Sessions))
	if r.Rework > 0 {
		fmt.Printf("  %d said so (still, did not help, an error named), %d pasted an error back, %d sent the same request again\n",
			r.By[effect.Said], r.By[effect.Pasted], r.By[effect.Again])
	}
	if afterRestore.Replies > 0 {
		fmt.Printf("  in sittings that began from a restore: %d of %d; in the rest: %d of %d\n",
			afterRestore.Rework, afterRestore.Replies, r.Rework-afterRestore.Rework, r.Replies-afterRestore.Replies)
	}
	fmt.Println("  Found by wording and by the shape of pasted output, with no model: lists checked against turns marked by hand")
	fmt.Println("  on one machine, where they found about nine in ten and were right about as often. \"Sent again\" is the least")
	fmt.Println("  sure of the three. The rate is the agent's and the task's as much as Logos's: the part Logos can move is the")
	fmt.Println("  fix that had already been tried and recorded as a dead end.")
}

func printRepeats(r effect.Repeats, heldNote string) {
	fmt.Println()
	fmt.Println("corrections — turns that open by calling the agent back (no, don't, stop, I said …) and say what was wanted")
	if r.Corrections == 0 {
		fmt.Printf("· none seen in %d session%s — which is not none made. They are found by how a turn opens, and a correction\n",
			r.Sessions, pluralS(r.Sessions))
		fmt.Println("  given as a plain instruction (\"use the staging config\") opens like any request: against turns marked by hand")
		fmt.Println("  on one machine, where all six corrections were of that kind, this found none of them.")
		return
	}
	fmt.Printf("· %d correction%s in %d of %d session%s — found by how a turn opens, which misses one given as a plain\n",
		r.Corrections, pluralS(r.Corrections), r.Corrected, r.Sessions, pluralS(r.Sessions))
	fmt.Println("  instruction, so a floor and a place to look, not a tally")
	if heldNote != "" {
		fmt.Printf("· which of them repeat something Logos held is unknown: %s\n", heldNote)
		return
	}
	share := ""
	if rate, ok := r.Rate(); ok {
		share = fmt.Sprintf(" (%.0f%%)", rate*100)
	}
	fmt.Printf("· %d of %d%s restate something Logos already held when the session began — the repeat-correction rate,\n",
		r.Repeated, r.Corrections, share)
	fmt.Println("  and the number that should fall as the vault learns you. Matched on shared words, at the bar before_you_try")
	fmt.Println("  uses, so each is listed with what it matched: read them before trusting the count.")
	for _, c := range r.Each {
		fmt.Printf("    %s  %-11s %-20s %s: %s\n", time.Unix(c.At, 0).Format("2006-01-02"), c.Harness, nameOr(c.Project),
			c.Known.Ref, text.Ellipsize(strings.Join(strings.Fields(c.Known.Text), " "), 90))
	}
}

// versus words a restored median against a cold one: "12 fewer words from
// you", "3 more lookups", "the same lookups".
func versus(restored, cold float64, unit, of string) string {
	if of != "" {
		of = " " + of
	}
	d := restored - cold
	switch {
	case d == 0:
		return "the same " + unit + "s" + of
	case d < 0:
		return fmt.Sprintf("%s fewer %s%s%s", fmtMedian(-d), unit, pluralF(-d), of)
	}
	return fmt.Sprintf("%s more %s%s%s", fmtMedian(d), unit, pluralF(d), of)
}

func pluralF(n float64) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// fmtMedian prints a median of counts: whole, or the half an even number of
// sessions leaves.
func fmtMedian(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.1f", v)
}

func harnessCounts(per map[string]int) string {
	names := make([]string, 0, len(per))
	for n := range per {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s %d", n, per[n]))
	}
	return strings.Join(parts, ", ")
}

func printTranscriptProblems(problems []string) {
	if len(problems) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "· %d transcript%s could not be read and %s in none of the counts above:\n",
		len(problems), pluralS(len(problems)), map[bool]string{true: "is", false: "are"}[len(problems) == 1])
	for i, p := range problems {
		if i == 5 {
			fmt.Fprintf(os.Stderr, "    … and %d more\n", len(problems)-i)
			break
		}
		fmt.Fprintf(os.Stderr, "    %s\n", p)
	}
}

// turnCitation is the "turn 12" a distilled entry carries, in the spellings
// the distiller's filter accepts. It says where an entry came from, not what
// it is about, and "turn" is long enough to count as a shared word.
var turnCitation = regexp.MustCompile(`\s*[\[(]?\bturns? \d+(?:(?:,| and|,? &) ?\d+)*[\])]?`)

// heldBy gathers what Logos held and when it first held it: every active
// memory, and what each checkpoint recorded as decided or as asked for. The
// note is why memories could not be read, when they could not — said beside
// the count it weakens, since a rate over half the record reads the same as a
// rate over all of it.
func heldBy(v string) (effect.Held, string) {
	var known []effect.Known
	note := ""
	if _, err := os.Stat(filepath.Join(v, ".logos", "index.db")); err != nil {
		note = "there is no index to read memories from — `logos index` builds it"
	} else if ix, err := index.Open(v); err != nil {
		note = "opening the index: " + err.Error()
	} else {
		defer ix.Close()
		// Init first, as every command that reads memories does: an index
		// built before anything was remembered has no table to read.
		err := memory.Init(ix.DB)
		var mems []memory.Memory
		if err == nil {
			mems, err = memory.All(ix.DB)
		}
		if err != nil {
			note = "reading memories: " + err.Error()
		}
		for _, m := range mems {
			known = append(known, effect.Known{Ref: fmt.Sprintf("memory %d", m.ID), Text: m.Text, Project: m.Project, Since: m.Created})
		}
	}
	scopes, err := session.Scopes(v)
	if err != nil && note == "" {
		note = "reading checkpoints: " + err.Error()
	}
	for _, sc := range scopes {
		cps, err := session.History(v, sc, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "· could not read the checkpoints of %s: %v\n", sc, err)
			continue
		}
		for _, c := range cps {
			for _, d := range c.Decisions {
				known = append(known, effect.Known{Ref: c.Slug, Text: d, Project: sc, Since: c.TS})
			}
			if c.Inferred == nil {
				continue
			}
			since := c.Inferred.At
			if since == 0 {
				since = c.TS
			}
			for _, w := range c.Inferred.Wanted {
				known = append(known, effect.Known{Ref: c.Slug, Text: turnCitation.ReplaceAllString(w, ""), Project: sc, Since: since})
			}
		}
	}
	return effect.Hold(known, usagepkg.SameProject), note
}
