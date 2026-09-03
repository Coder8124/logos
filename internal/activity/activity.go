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

// Append writes one event. It creates the month's file if needed and never
// rewrites what is already there.
//
// Errors are returned rather than swallowed, but callers on a hook path should
// treat them as advisory: a log that fails to write must not break the session
// it is recording. The hook decides that; this function only reports.
func Append(vault string, e Event) error {
	if e.TS == 0 {
		e.TS = time.Now().Unix()
	}
	if strings.TrimSpace(e.Kind) == "" {
		return fmt.Errorf("activity: an event needs a kind")
	}
	dir := filepath.Join(vault, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	f, err := os.OpenFile(monthFile(dir, e.TS), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
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
