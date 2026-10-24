// Package usage is the ledger `logos usage` reads: what each context pack cost
// against what it drew from, and each time a recorded dead end was handed back
// to an agent about to retry it.
//
// Every number here is a receipt, never an estimate of something Logos cannot
// see. It cannot see a host's API usage, so "tokens saved" is a pack's size
// against its own uncut candidates, both measured by contextpack on the same
// estimate. It cannot tell whether an agent heeded a dead end, so the count is
// of dead ends returned, not of mistakes avoided.
//
// JSONL in the vault, one file per month, for the reasons internal/activity
// gives: a person can read it with grep, and it survives the index being
// deleted. Totals kept only in SQLite would be the fifth time a durable record
// silently lived in the cache. Unlike the activity log it holds no prompt and
// no tool output — a timestamp, a project name and counts — so it is kept, not
// aged out: a running total that forgets its first month is not a total.
package usage

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/contextpack"
	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/procedure"
	"github.com/Coder8124/logos/internal/scope"
	"github.com/Coder8124/logos/internal/textmatch"
	vaultpkg "github.com/Coder8124/logos/internal/vault"
)

// Dir is the vault subdirectory holding the ledger.
const Dir = "usage"

// Event kinds.
const (
	KindPack    = "pack"     // a context pack was handed to an agent
	KindDeadEnd = "dead-end" // a recorded dead end was handed back before a retry
	KindAtEdit  = "at-edit"  // a ruling naming a file was shown as that file was edited
	KindAvoided = "avoided"  // a session ended without touching what a ruling it was shown names
	// A procedure's evidence is earned here, not in its text: the text is
	// what its uid is derived from, so rewriting "evidence: once" would make
	// it a different memory, and the ledger is already the vault's record of
	// what was shown to whom.
	KindProcedure = "procedure" // procedures known to work were handed back before a try
	KindProven    = "proven"    // a procedure's verify passed in the session after it was shown
)

// Week is how far back `logos usage --week` reads.
const Week = 7 * 24 * time.Hour

// Event is one line of the ledger.
type Event struct {
	TS      int64  `json:"ts"`
	Kind    string `json:"kind"`
	Project string `json:"project,omitempty"`
	// Via is where it happened, "mcp:resume" or "cli:tried", so a total can be
	// traced back to the path that produced it.
	Via string `json:"via"`
	// Sent and Full are a pack's tokens as sent and as its candidates came to
	// uncut, the same headings and footer counted in both.
	Sent int `json:"sent,omitempty"`
	Full int `json:"full,omitempty"`
	// Rulings is how many recorded dead ends a dead-end event returned, or an
	// avoided event counts.
	Rulings int `json:"rulings,omitempty"`
	// Reader is who a pack was built for and From who wrote the checkpoint it
	// handed over — the two names a handoff is counted from. Agent names, as
	// the checkpoint already records them.
	Reader string `json:"reader,omitempty"`
	From   string `json:"from,omitempty"`
	// Session is the host's session the event happened in, when the caller
	// knows it, so the session-end receipt can find what was shown to it.
	Session string `json:"session,omitempty"`
	// Refs name the rulings handed back, as Ref of their text: an opaque
	// fingerprint, not the text, so the ledger still holds no content. The
	// receipt resolves them against the vault at session end.
	Refs []string `json:"refs,omitempty"`
}

// Ref is the ledger's name for a ruling: a fingerprint of its whole recorded
// text (Record.Raw, not the route alone, which two rulings can share), spacing
// and case aside, so the same ruling read from two places has one name.
func Ref(text string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.Join(strings.Fields(text), " "))))
	return hex.EncodeToString(sum[:6])
}

// offMarker turns the ledger off. It sits in the vault, beside the ledger, not
// in .logos/: deleting the index is documented as lossless, and a ledger that
// came back on after an index rebuild would break that promise.
const offMarker = "recording-off"

// Recording reports whether this vault's ledger takes new events. On unless
// someone said otherwise — it holds counts, never prompts or content — but
// everything Logos writes on its own has a switch, with `logos usage off` for
// the vault and LOGOS_USAGE=off for one process.
func Recording(vault string) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("LOGOS_USAGE")), "off") {
		return false
	}
	_, err := os.Stat(filepath.Join(vault, Dir, offMarker))
	return err != nil
}

// SetRecording turns the ledger on or off for this vault. What is already
// recorded is left alone either way; the files are the user's to delete.
func SetRecording(vault string, on bool) error {
	off := filepath.Join(vault, Dir, offMarker)
	if on {
		if err := os.Remove(off); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := vaultpkg.MkdirPrivate(filepath.Join(vault, Dir)); err != nil {
		return err
	}
	return os.WriteFile(off, []byte("The usage ledger is off for this vault. `logos stats --usage on` turns it back on.\n"), vaultpkg.FileMode)
}

// Record appends one event.
//
// Errors are returned, and a caller reports rather than swallows them: a
// ledger that silently stopped being written would show a total that looks
// complete and is not. They must not fail the pack or the check being recorded,
// which already did its job.
func Record(vault string, e Event) error {
	if strings.TrimSpace(vault) == "" {
		return fmt.Errorf("usage: no vault to record in")
	}
	if e.TS == 0 {
		e.TS = time.Now().Unix()
	}
	if e.Kind == "" {
		return fmt.Errorf("usage: an event needs a kind")
	}
	if !Recording(vault) {
		// Off is the user's answer, not a failure: nothing to report.
		return nil
	}
	e.Project = projectKey(e.Project)
	dir := filepath.Join(vault, Dir)
	if err := vaultpkg.MkdirPrivate(dir); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	// One short O_APPEND write per event, so a server and a hook recording at
	// once interleave whole lines, never halves of one.
	path := filepath.Join(dir, time.Unix(e.TS, 0).Format("2006-01")+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, vaultpkg.FileMode)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// RulingRefs are the refs of rulings a dead-end check handed back.
func RulingRefs(hits []deadend.Ruling) []string {
	var out []string
	for _, h := range hits {
		out = append(out, Ref(h.Record.Raw))
	}
	return out
}

// ProcedureRefs are the refs of procedures a check handed back.
func ProcedureRefs(hits []procedure.Hit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, Ref(h.Memory))
	}
	return out
}

// Tally is what the ledger says about one procedure: how many times it was
// handed back, and in how many sessions its verify then passed.
type Tally struct {
	Shown  int
	Proven int
}

// Tallies counts, by Ref of a procedure's text, what the ledger recorded of
// procedures being shown and proven.
func Tallies(events []Event) map[string]Tally {
	out := map[string]Tally{}
	for _, e := range events {
		for _, r := range e.Refs {
			t := out[r]
			switch e.Kind {
			case KindProcedure:
				t.Shown++
			case KindProven:
				t.Proven++
			default:
				continue
			}
			out[r] = t
		}
	}
	return out
}

// RecordPack records a rendered pack. Its headings and footer are counted on
// both sides, so the saving is exactly what the budget left out. session is
// the host's session id when the caller has one, "" otherwise.
//
// The refs are the handed-over checkpoint's own ruled-out approaches, the ones
// a pack always shows under "Already tried". Earlier checkpoints' rulings are
// left out: the budget may cut them, and a receipt claiming a ruling was shown
// when it was cut would overstate what Logos did.
func RecordPack(vault, via, project string, pack contextpack.Pack, session string) error {
	b := pack.Budget
	e := Event{
		Kind: KindPack, Project: project, Via: via, Session: session, Reader: pack.Reader,
		Sent: b.Spent + b.Overhead, Full: b.Candidates + b.Overhead,
	}
	if c := pack.Checkpoint; c != nil {
		e.From = c.Agent
		for _, f := range c.Failed {
			// Keyed as Collect reads it, so the receipt can find it again.
			e.Refs = append(e.Refs, Ref(deadend.ParseRecord(textmatch.Flatten(f)).Raw))
		}
	}
	return Record(vault, e)
}

// Read returns every event in the ledger, oldest first, and how many lines
// could not be read. A torn line costs that line, not the month — but the
// count is returned, because a total quietly missing lines is a total that is
// wrong without saying so.
func Read(vault string) ([]Event, int, error) {
	files, err := filepath.Glob(filepath.Join(vault, Dir, "*.jsonl"))
	if err != nil {
		return nil, 0, err
	}
	sort.Strings(files)
	var out []Event
	bad := 0
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return nil, 0, err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "" {
				continue
			}
			var e Event
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil || e.Kind == "" {
				bad++
				continue
			}
			out = append(out, e)
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, 0, fmt.Errorf("reading %s: %w", path, err)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out, bad, nil
}

// Totals is the ledger summed.
type Totals struct {
	Packs int
	Sent  int
	Full  int
	// DeadEndChecks is the number of checks that returned at least one recorded
	// dead end, and Rulings the dead ends they returned between them.
	DeadEndChecks int
	Rulings       int
	// AtEdit is how many rulings an edit hook put in front of an agent as it
	// opened a file they name — kept apart from Rulings, which an agent asked for.
	AtEdit int
	// Restores is how many packs were a resume, and Handoffs how many of those
	// handed over a checkpoint another agent wrote — see Handoff.
	Restores int
	Handoffs int
	// Avoided is how many rulings sessions were shown and then never touched
	// what they name: "may have avoided", never "avoided".
	Avoided int
	Since   int64
}

// Saved is what the packs' budgets left out, in tokens.
func (t Totals) Saved() int { return t.Full - t.Sent }

// Sum totals the events for one project, or for all of them when project is "".
func Sum(events []Event, project string) Totals {
	project = projectKey(project)
	var t Totals
	for _, e := range events {
		if project != "" && projectKey(e.Project) != projectKey(project) {
			continue
		}
		if t.Since == 0 || e.TS < t.Since {
			t.Since = e.TS
		}
		switch e.Kind {
		case KindPack:
			t.Packs++
			t.Sent += e.Sent
			t.Full += e.Full
			if isResume(e) {
				t.Restores++
			}
			if Handoff(e) {
				t.Handoffs++
			}
		case KindAvoided:
			t.Avoided += e.Rulings
		case KindDeadEnd:
			t.DeadEndChecks++
			t.Rulings += e.Rulings
		case KindAtEdit:
			t.AtEdit += e.Rulings
		}
	}
	return t
}

// Within is the events at or after from, a Unix time.
func Within(events []Event, from int64) []Event {
	var out []Event
	for _, e := range events {
		if e.TS >= from {
			out = append(out, e)
		}
	}
	return out
}

func isResume(e Event) bool { return e.Kind == KindPack && strings.HasSuffix(e.Via, ":resume") }

// Handoff reports whether e is a resume that handed one agent's checkpoint to
// another. Both names must be known: "cli" is what a shell gets when nothing
// said who is asking, and counting it as different from every agent would
// make each person's own resume a handoff — on either side, since a
// checkpoint written from a shell is signed "cli" too.
func Handoff(e Event) bool {
	if !isResume(e) || e.From == "" || e.From == "cli" || e.Reader == "" || e.Reader == "cli" {
		return false
	}
	return e.Reader != e.From
}

// projectKey is the one name an event is filed and summed under. The callers
// spell a project three ways — the bare name a pack is built for, the worktree
// scope a dead-end check runs in, the path someone typed after --project — and
// recording each as given split one project's total across three keys, with
// `logos usage kestrel` counting only one of them. A path is its project's
// name, and a worktree's work is its project's: the total is per project.
// Applied on read as well, so a ledger written before this still sums right.
// SameProject reports whether two spellings of a project are filed under one
// name — a worktree scope and its project, a path and its name.
func SameProject(a, b string) bool { return projectKey(a) == projectKey(b) }

func projectKey(p string) string {
	p = scope.NormalizeArg(p)
	if i := strings.Index(p, "/"); i > 0 {
		p = p[:i]
	}
	return p
}
