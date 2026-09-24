package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StillOpen reports, for a harness, which of its transcripts belong to a
// session that is still running. A harness that does not say is never open:
// the caller falls back to how long a transcript has gone unwritten.
//
// That fallback is a guess, and a bad one for a window left idle: past it, a
// session still open was recorded as ended, and the work it went on to do was
// never recorded, because its transcript was already settled. Claude Code
// keeps a file per running process, in sessions/ beside projects/, naming the
// session the process is on — so for it this is a fact, not a guess.
func StillOpen(harness string) func(path string) bool {
	if harness != "claude-code" {
		return func(string) bool { return false }
	}
	root := claudeCodeReader{}.root()
	if root == "" {
		return func(string) bool { return false }
	}
	open := claudeCodeOpen(filepath.Join(filepath.Dir(root), "sessions"))
	return func(path string) bool {
		return open[strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))]
	}
}

// claudeCodeOpen is the ids of the sessions Claude Code's live processes are
// on. A file whose process has gone — a host that was killed leaves its file
// behind — names a session that has ended, which is exactly the one the sweep
// is for, so each is checked against the process it names.
func claudeCodeOpen(dir string) map[string]bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	open := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var f struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			ProcStart string `json:"procStart"`
		}
		if json.Unmarshal(raw, &f) != nil || f.PID <= 0 || f.SessionID == "" {
			continue
		}
		// procStart is the process's start in UTC, and what tells a live
		// Claude Code from an unrelated process that was given its pid after
		// it died. Where this platform cannot say when a process started, a
		// live pid is taken as the session's own: the cost of being wrong is a
		// record written later, not one never written.
		want, perr := time.Parse("Mon Jan _2 15:04:05 2006", f.ProcStart)
		if !processAlive(f.PID) {
			continue
		}
		if got, ok := processStart(f.PID); ok && perr == nil && absDuration(got.Sub(want)) > 2*time.Second {
			continue
		}
		open[f.SessionID] = true
	}
	return open
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
