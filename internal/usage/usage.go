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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/contextpack"
	vaultpkg "github.com/Coder8124/logos/internal/vault"
)

// Dir is the vault subdirectory holding the ledger.
const Dir = "usage"

// Event kinds.
const (
	KindPack    = "pack"     // a context pack was handed to an agent
	KindDeadEnd = "dead-end" // a recorded dead end was handed back before a retry
)

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
	// Rulings is how many recorded dead ends a dead-end event returned.
	Rulings int `json:"rulings,omitempty"`
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

// RecordPack records a rendered pack. Its headings and footer are counted on
// both sides, so the saving is exactly what the budget left out.
func RecordPack(vault, via, project string, b contextpack.Budget) error {
	return Record(vault, Event{
		Kind: KindPack, Project: project, Via: via,
		Sent: b.Spent + b.Overhead, Full: b.Candidates + b.Overhead,
	})
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
	Since         int64
}

// Saved is what the packs' budgets left out, in tokens.
func (t Totals) Saved() int { return t.Full - t.Sent }

// Sum totals the events for one project, or for all of them when project is "".
func Sum(events []Event, project string) Totals {
	var t Totals
	for _, e := range events {
		if project != "" && e.Project != project {
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
		case KindDeadEnd:
			t.DeadEndChecks++
			t.Rulings += e.Rulings
		}
	}
	return t
}
