package auto

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	vaultpkg "github.com/Coder8124/logos/internal/vault"
)

// LogFile is what auto did, one line per action, at the vault's root beside
// auto.md. Only what happened out of the user's sight is logged — a session
// end, a server shutting down, a background pass. What a command does while
// they watch it says so on the spot and needs no second telling.
//
// Markdown, append-only, in the vault: the next resume reads it, and a log in
// the index would be emptied by the very rebuild it might be reporting.
const LogFile = "auto-log.md"

// LogPath is where a vault's auto log is written.
func LogPath(vault string) string { return filepath.Join(vault, LogFile) }

// Entry is one thing auto did, or failed to do.
type Entry struct {
	At      time.Time
	Switch  Switch
	Project string
	N       int
	Failed  string
}

// Record appends one entry to the log. The caller still reports a failure
// where it happened; this is so the next resume can say it too, since the
// place it happened — a hook, a server going away — had nobody watching.
func Record(vault string, e Entry) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	line := fmt.Sprintf("- %s %s", e.At.UTC().Format(time.RFC3339), e.Switch)
	if e.Project != "" {
		line += " project=" + oneWord(e.Project)
	}
	if e.Failed != "" {
		line += " failed: " + strings.Join(strings.Fields(e.Failed), " ")
	} else {
		line += " n=" + strconv.Itoa(e.N)
	}
	path := LogPath(vault)
	_, statErr := os.Stat(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, vaultpkg.FileMode)
	if err != nil {
		return err
	}
	if errors.Is(statErr, os.ErrNotExist) {
		line = "# What Logos did on its own\n\nAppended by Logos; `logos auto status` reads it.\n\n" + line
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// oneWord keeps a project name from breaking the line it is written into.
func oneWord(s string) string { return strings.Join(strings.Fields(s), "_") }

var logLine = regexp.MustCompile(`^- (\S+) ([\w-]+)(?: project=(\S+))?(?: n=(\d+)| failed: (.*))$`)

// Entries reads the log for one project, and the vault-wide entries with none,
// from since on; project "" reads every project's. A line it cannot read is skipped: the log is Logos's own
// account, and one mangled line must not hide the rest of it.
func Entries(vault, project string, since time.Time) ([]Entry, error) {
	f, err := os.Open(LogPath(vault))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		m := logLine.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		if m == nil {
			continue
		}
		at, err := time.Parse(time.RFC3339, m[1])
		if err != nil || at.Before(since) {
			continue
		}
		if project != "" && m[3] != "" && m[3] != oneWord(project) {
			continue
		}
		n, _ := strconv.Atoi(m[4])
		out = append(out, Entry{At: at, Switch: Switch(m[2]), Project: m[3], N: n, Failed: m[5]})
	}
	return out, sc.Err()
}

// did is how resume words a count of one switch's actions.
func did(s Switch, n int) string {
	switch s {
	case Checkpoint:
		return fmt.Sprintf("checkpointed %d session%s", n, plural(n))
	}
	return fmt.Sprintf("%s ran %d time%s", s, n, plural(n))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Summary is the one line resume opens with: what auto did since, by switch,
// then each failure, or "" when it did nothing. Failures are said one by one
// and never summed — two different reasons are two things to fix.
func Summary(entries []Entry) string {
	var order []Switch
	count := map[Switch]int{}
	var failed []string
	for _, e := range entries {
		if e.Failed != "" {
			failed = append(failed, fmt.Sprintf("%s failed (%s)", e.Switch, e.Failed))
			continue
		}
		if _, ok := count[e.Switch]; !ok {
			order = append(order, e.Switch)
		}
		count[e.Switch] += e.N
	}
	var parts []string
	for _, s := range order {
		if count[s] > 0 {
			parts = append(parts, did(s, count[s]))
		}
	}
	parts = append(parts, failed...)
	if len(parts) == 0 {
		return ""
	}
	return "auto: " + strings.Join(parts, ", ") + " · `logos auto status`"
}
