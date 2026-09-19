// Package activity is the append-only record of what an agent actually did.
//
// Everything else in this system is something a model chose to tell us. A
// memory is stored because the agent called `remember`; a checkpoint exists
// because it called `checkpoint`. That works right up until the model does not
// call them, and the failure is silent — a session that decided three things
// and wrote none of them down looks, from the vault, exactly like a session
// that never happened.
//
// This is the other half. It records events the agent does not get a vote on:
// the prompts a person typed, the tools that ran, the turns that ended. The
// host reports them through hooks whether or not the model would have thought
// to mention them, which makes this the one part of the record that cannot be
// forgotten.
//
// Three deliberate choices:
//
// JSONL on disk, one file per month. Not SQLite. The log is meant to be read by
// things that are not us — grep, jq, a script, the user at three in the morning
// wondering what happened. A database would be faster to query and would make
// that impossible, and "your history is a file you own" stops being true the
// moment the only reader is our binary. Monthly files keep `activity/2026-09.jsonl`
// a name a person can guess and keep any single file bounded.
//
// Append-only, never rewritten. A log that edits itself is not evidence.
//
// No model, ever. Recording is arithmetic and string handling. A summariser
// here would turn a factual record into a plausible one, and cost a hook budget
// it does not have.
package activity

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	vaultpkg "github.com/Coder8124/logos/internal/vault"
)

// Dir is the vault subdirectory holding the log.
const Dir = "activity"

// Event kinds. Plain strings, so a host that grows a new event type does not
// need a migration here — an unknown kind still records, still greps, and still
// prints. The set below is what Claude Code's hooks can currently tell us.
const (
	KindPrompt  = "prompt"   // a person typed something
	KindTool    = "tool"     // a tool ran and returned
	KindTurnEnd = "turn-end" // the agent finished a turn
	KindNotice  = "notice"   // the host wants the user's attention
	KindStart   = "session-start"
	KindEnd     = "session-end"
	KindBlocked = "permission" // the agent asked to do something and is waiting
	KindWarn    = "warning"    // something the hook path could not report any louder
)

// Event is one line of the log.
//
// The fields are small and fixed on purpose. This file is appended to from a
// hook on the critical path of every tool call, so it holds what is cheap to
// know and true without interpretation — and puts anything host-specific in
// Extra rather than growing a column per host.
type Event struct {
	TS      int64  `json:"ts"`
	Kind    string `json:"kind"`
	Project string `json:"project,omitempty"`
	Session string `json:"session,omitempty"`
	// Agent is the host that reported the event ("claude-code"), not the model.
	Agent string `json:"agent,omitempty"`
	// Tool is the tool's name for KindTool, empty otherwise.
	Tool string `json:"tool,omitempty"`
	// Summary is one line a human can read without expanding anything. It is
	// the whole point of the row; a log whose rows need decoding is a log
	// nobody scans.
	Summary string `json:"summary,omitempty"`
	// Extra carries whatever the host sent that we did not model. Kept so the
	// record stays lossless even where our schema is not.
	Extra map[string]any `json:"extra,omitempty"`
}

// offMarker names the file that turns recording off. It sits in the vault, not
// in .logos/, because deleting the index is documented as lossless and a log
// that came back on after an index rebuild would be the loudest possible way
// to break that promise.
const offMarker = "recording-off"

// onMarker records that someone said yes to this. Recording the log is the one
// thing in the vault the user did not write a line of, so it is the one thing
// that has to be asked for rather than assumed.
const onMarker = "recording-on"

// Recording reports whether this vault accepts new activity events.
//
// Three states, not two, because there are three genuinely different vaults.
//
//	off marker  — the user said no. Never record.
//	on marker   — the user said yes, at setup or with `logos activity on`.
//	neither     — nobody was ever asked.
//
// The last is the interesting one. Defaulting it to "record" is what #88 was
// about: every prompt and tool call written down forever, disclosed nowhere,
// while the README said "nothing is observed". Defaulting it to "don't" would
// silently switch off the installs that have been recording since the plugin
// shipped, and a user who has been relying on `logos activity` for a fortnight
// should not find it empty because we changed our minds.
//
// So an un-asked vault is grandfathered on the evidence: if the log already has
// events, this install was recording before the question existed and keeps
// doing it. If it does not, nobody has anything invested and the answer is no
// until someone says otherwise. New installs are opt-in; existing ones are
// undisturbed; neither is decided silently against the user.
func Recording(vault string) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("LOGOS_ACTIVITY")), "off") {
		return false
	}
	dir := filepath.Join(vault, Dir)
	if _, err := os.Stat(filepath.Join(dir, offMarker)); err == nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, onMarker)); err == nil {
		return true
	}
	return hasEvents(dir)
}

// hasEvents reports whether this log was already being written before anyone
// was asked about it. See Recording.
func hasEvents(dir string) bool {
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return false
	}
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && st.Size() > 0 {
			return true
		}
	}
	return false
}

// Decided reports whether this vault has an answer of its own, so setup can ask
// once and never again. A vault grandfathered on its existing log counts as
// decided: it has been recording for weeks, and re-offering the switch every
// time setup is re-run is how a user flips it by accident.
func Decided(vault string) bool {
	dir := filepath.Join(vault, Dir)
	for _, m := range []string{offMarker, onMarker} {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return hasEvents(dir)
}

// SetRecording turns the log on or off for this vault.
//
// Either answer leaves a marker, because the third state is "nobody has been
// asked" and an explicit yes has to be distinguishable from it — otherwise
// saying yes on an empty vault would record nothing and read as a broken
// switch.
func SetRecording(vault string, on bool) error {
	dir := filepath.Join(vault, Dir)
	if err := vaultpkg.MkdirPrivate(dir); err != nil {
		return err
	}
	off := filepath.Join(dir, offMarker)
	if on {
		if err := os.Remove(off); err != nil && !os.IsNotExist(err) {
			return err
		}
		return os.WriteFile(filepath.Join(dir, onMarker), []byte("logos activity is on for this vault; `logos activity off` stops it\n"), vaultpkg.FileMode)
	}
	if err := os.Remove(filepath.Join(dir, onMarker)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(off, []byte("logos activity is off for this vault; `logos activity on` resumes it\n"), vaultpkg.FileMode)
}

// disclosedMarker records that this vault has told its user what the log does.
// Beside the log rather than in .logos/ for the same reason as offMarker, and
// because a rebuilt index re-disclosing is a smaller failure than a rebuilt
// index un-disclosing.
const disclosedMarker = ".disclosed"

// Disclose returns the one-time sentence telling the user their prompts and
// tool calls are being written down, and where, and how to stop — empty once it
// has been said, or when there is nothing being recorded to disclose.
//
// Asking is not saying. The caller prints it and then calls MarkDisclosed, so a
// crash in between repeats the notice rather than losing it. The other order
// reads tidier and is wrong: this is the one notice where a user who never sees
// it has been recorded without being told, which no amount of care about the
// redaction makes acceptable, and a disclosure shown twice costs only a line.
func Disclose(vault string) (string, error) {
	if !Recording(vault) {
		return "", nil
	}
	dir := filepath.Join(vault, Dir)
	if _, err := os.Stat(filepath.Join(dir, disclosedMarker)); err == nil {
		return "", nil
	}
	return fmt.Sprintf("logos writes a log of your prompts and tool calls to %s, on this machine only; `logos activity off` stops it and `logos activity` shows it.", dir), nil
}

// MarkDisclosed records that the sentence Disclose returned has actually been
// delivered. Separate from Disclose so that the marking happens after the
// saying, never before it.
func MarkDisclosed(vault string) error {
	dir := filepath.Join(vault, Dir)
	if err := vaultpkg.MkdirPrivate(dir); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, disclosedMarker), []byte("the user has been told what the activity log records\n"), vaultpkg.FileMode)
}

// Append writes one event. It creates the month's file if needed and never
// rewrites what is already there.
//
// Errors are returned rather than swallowed, but callers on a hook path should
// treat them as advisory: a log that fails to write must not break the session
// it is recording. The hook decides that; this function only reports.
func Append(vault string, e Event) error {
	// Checked here rather than at each caller: the hooks, the CLI and the
	// server all reach the log through this one door, and an off switch with a
	// way around it is not one.
	if !Recording(vault) {
		return nil
	}
	if e.TS == 0 {
		e.TS = time.Now().Unix()
	}
	if strings.TrimSpace(e.Kind) == "" {
		return fmt.Errorf("activity: an event needs a kind")
	}
	dir := filepath.Join(vault, Dir)
	if err := vaultpkg.MkdirPrivate(dir); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	// O_APPEND on a single write under the size of a pipe buffer is atomic
	// enough for concurrent hooks: two agents in two terminals will interleave
	// whole lines, never half of one. That is the property that matters — a
	// torn line would poison every reader of the file, including jq.
	path := monthFile(dir, e.TS)
	// A new month's file is the one moment worth spending a directory scan on,
	// and it is the moment the oldest month has just aged out. Doing it here
	// rather than on a timer means retention needs no daemon and no `logos
	// prune` nobody runs — the log prunes itself by being written to.
	_, statErr := os.Stat(path)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, vaultpkg.FileMode)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	if os.IsNotExist(statErr) {
		// Advisory: failing to delete an old month is not a reason to fail the
		// event that was just written.
		_ = prune(dir, time.Unix(e.TS, 0))
	}
	return nil
}

// Retention is how long a month's log is kept.
//
// The log records every prompt and every tool call a host reported, which is
// the most sensitive thing in the vault and the one part of it the user never
// chose line by line. Keeping it forever was never a decision anyone made — it
// was the absence of one, and `logos activity` said the log "rolls off under
// capture retention" while nothing pruned anything.
//
// Thirty days measured from the end of the month a file covers, so a file is
// deleted only once everything in it is past the window. Whole files, never
// lines: rewriting a month to drop its first week would make the log something
// that edits itself, and an append-only record is the only kind that is
// evidence.
const Retention = 30 * 24 * time.Hour

func prune(dir string, now time.Time) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return err
	}
	var last error
	for _, f := range files {
		month, err := time.Parse("2006-01", strings.TrimSuffix(filepath.Base(f), ".jsonl"))
		if err != nil {
			continue // not a month file; not ours to delete
		}
		// The instant after the last second the file can hold.
		end := month.AddDate(0, 1, 0)
		if now.Sub(end) > Retention {
			if err := os.Remove(f); err != nil {
				last = err
			}
		}
	}
	return last
}

func monthFile(dir string, ts int64) string {
	return filepath.Join(dir, time.Unix(ts, 0).Format("2006-01")+".jsonl")
}

// Query narrows the log. A zero value matches everything.
type Query struct {
	Project string
	Kind    string
	Tool    string
	Since   time.Time
	Until   time.Time
	// Limit caps the result, keeping the *newest* matches. A limit applied
	// while scanning would keep the oldest, which is the opposite of what
	// anyone asking for "the last 20" means.
	Limit int
}

func (q Query) match(e Event) bool {
	if q.Project != "" && e.Project != q.Project {
		return false
	}
	if q.Kind != "" && e.Kind != q.Kind {
		return false
	}
	if q.Tool != "" && !strings.EqualFold(e.Tool, q.Tool) {
		return false
	}
	if !q.Since.IsZero() && e.TS < q.Since.Unix() {
		return false
	}
	if !q.Until.IsZero() && e.TS > q.Until.Unix() {
		return false
	}
	return true
}

// Read returns matching events, newest first.
//
// A malformed line is skipped rather than fatal. The log is appended to by
// hooks running in whatever shell the user has, and one truncated line from a
// machine that lost power should cost that line, not the month.
func Read(vault string, q Query) ([]Event, error) {
	files, err := filepath.Glob(filepath.Join(vault, Dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []Event
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		// Tool results can be long; the default 64KB token limit would turn a
		// big one into a scan error and silently end the file early.
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			var e Event
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				continue
			}
			if q.match(e) {
				out = append(out, e)
			}
		}
		f.Close()
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS > out[j].TS })
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// Projects lists the projects that appear in the log, busiest first. This is
// what makes the log browsable without knowing what to ask for.
func Projects(vault string) ([]string, error) {
	all, err := Read(vault, Query{})
	if err != nil {
		return nil, err
	}
	n := map[string]int{}
	for _, e := range all {
		if e.Project != "" {
			n[e.Project]++
		}
	}
	out := make([]string, 0, len(n))
	for p := range n {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if n[out[i]] != n[out[j]] {
			return n[out[i]] > n[out[j]]
		}
		return out[i] < out[j]
	})
	return out, nil
}
