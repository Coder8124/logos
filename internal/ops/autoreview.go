package ops

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/procedure"
	"github.com/Coder8124/logos/internal/secret"
	"github.com/Coder8124/logos/internal/transcript"
)

// The review switch: accepting from the review queue what a mechanical rule is
// sure of, so a person reviews only what needs a person. No model decides —
// the rule is the same for every user and can be read here in full. A queued
// memory is accepted only when every one of these holds:
//
//   - it cites a transcript turn (TurnRef) that is still there to be read, so
//     what it claims can be checked against what was actually said;
//   - its text carries no secret redaction, since a memory with a hole where a
//     credential was is one a person should see;
//   - it contested nothing when it arrived and contests nothing now, pinned
//     memories included: a dispute is the one thing the queue exists for;
//   - it is a decision ("Decided …" or "Decision: …", a fact or context) or a
//     procedure with a trap — the two kinds of memory a later agent pays most
//     for not having.
//
// Everything else stays in the queue.

// TurnRef is how a proposal cites the transcript turn it was made in: the
// harness, how many turns the transcript held, and its path. The path is last
// because it is the one part that can hold a colon.
func TurnRef(harness, path string, turns int) string {
	return fmt.Sprintf("%s:%d:%s", harness, turns, path)
}

func parseTurnRef(ref string) (harness, path string, turns int, ok bool) {
	harness, rest, ok1 := strings.Cut(ref, ":")
	n, path, ok2 := strings.Cut(rest, ":")
	turns, err := strconv.Atoi(n)
	if !ok1 || !ok2 || err != nil || harness == "" || path == "" || turns < 1 {
		return "", "", 0, false
	}
	return harness, path, turns, true
}

// readTranscript is the seam the tests fill: no real harness on this machine.
var readTranscript = transcript.ReadFile

// AutoReview runs the review switch over the queue and returns how many it
// accepted. Off, it does nothing at all. A memory it could not accept is
// reported, and the rest are still tried: one failure is not a reason to leave
// the others for a person who turned the switch on to be spared them.
func AutoReview(db *sql.DB, vault string, now time.Time) (int, error) {
	if !auto.On(vault, auto.Review) {
		return 0, nil
	}
	// A proposal whose line the user deleted is one they rejected; accepting
	// it from a stale cache would overrule them.
	if _, _, err := memory.ReconcilePending(db); err != nil {
		return 0, fmt.Errorf("could not read the user's edits to the review queue: %w", err)
	}
	pending, err := memory.Pending(db)
	if err != nil {
		return 0, err
	}
	turns := map[string]int{} // transcript read once per path, -1 when unreadable
	var accepted int
	var failed []string
	for _, m := range pending {
		if !autoAcceptable(db, m, turns) {
			continue
		}
		if err := memory.AcceptAuto(db, m.ID, now); err != nil {
			failed = append(failed, fmt.Sprintf("#%d: %v", m.ID, err))
			continue
		}
		accepted++
	}
	if len(failed) > 0 {
		return accepted, fmt.Errorf("could not accept %s", strings.Join(failed, "; "))
	}
	return accepted, nil
}

func autoAcceptable(db *sql.DB, m memory.Memory, turns map[string]int) bool {
	if m.Contests != 0 || strings.Contains(m.Text, secret.Marker) || !decisionOrTrap(m) {
		return false
	}
	if !turnExists(m.Turn, turns) {
		return false
	}
	_, contested := memory.ContestedNow(db, m.ID, m.Text)
	return !contested
}

func decisionOrTrap(m memory.Memory) bool {
	switch m.Kind {
	case memory.Fact, memory.Context:
		t := strings.ToLower(strings.TrimSpace(m.Text))
		return strings.HasPrefix(t, "decided") || strings.HasPrefix(t, "decision:")
	case memory.Procedure:
		return strings.TrimSpace(procedure.ParseRecord(m.Text).Trap) != ""
	}
	return false
}

func turnExists(ref string, turns map[string]int) bool {
	harness, path, n, ok := parseTurnRef(ref)
	if !ok {
		return false
	}
	key := harness + "\x00" + path
	have, seen := turns[key]
	if !seen {
		have = -1
		if ts, err := readTranscript(harness, path); err == nil && ts != nil {
			have = len(ts.Turns)
		}
		turns[key] = have
	}
	return have >= n
}

// AutoAcceptedSinceReview is how many memories the review switch accepted
// since a person last accepted or rejected one: the number every mention of the
// queue carries, so what was accepted for the user is never out of their sight.
func AutoAcceptedSinceReview(db *sql.DB) (int, error) {
	last, err := memory.LastReview(db)
	if err != nil {
		return 0, err
	}
	done, err := memory.AutoAccepted(db, last+1)
	return len(done), err
}

// RunAutoReview is the sweep as resume runs it: logged, so the auto line resume
// opens with says what it accepted, and a failure returned as a line to print,
// since resume has nobody to return an error to but the reader.
func RunAutoReview(db *sql.DB, vault, project string) string {
	n, err := AutoReview(db, vault, time.Now())
	if n > 0 {
		e := auto.Entry{Switch: auto.Review, Project: project, N: n}
		if rerr := auto.Record(vault, e); rerr != nil {
			return fmt.Sprintf("auto: accepted %d from the review queue, and could not log it: %v\n\n", n, rerr)
		}
	}
	if err != nil {
		return fmt.Sprintf("auto: the review switch %v\n\n", err)
	}
	return ""
}
