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

	"github.com/Coder8124/logos/internal/untrusted"
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

// Entry is one thing auto did, or failed to do. A run is Started, Failed, or
// done: N, or Counts for a switch whose run does several things, and Skipped
// when it could not do all of it for a reason that is not a fault.
type Entry struct {
	At      time.Time
	Switch  Switch
	Project string
	Started bool
	N       int
	Counts  []Count
	Skipped string
	Failed  string
}

// Count is one named number a run reports, such as dream's merged.
type Count struct {
	Name string
	N    int
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
	switch {
	case e.Started:
		line += " started"
	case e.Failed != "":
		line += " failed: " + oneLine(e.Failed)
	default:
		if len(e.Counts) == 0 || e.N != 0 {
			line += " n=" + strconv.Itoa(e.N)
		}
		for _, c := range e.Counts {
			line += fmt.Sprintf(" %s=%d", oneWord(c.Name), c.N)
		}
		if e.Skipped != "" {
			line += " skipped: " + oneLine(e.Skipped)
		}
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

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// oneWord keeps a project name from breaking the line it is written into.
func oneWord(s string) string { return strings.Join(strings.Fields(s), "_") }

// Entries reads the log for one project, and the vault-wide entries with none,
// from since on; project "" reads every project's. A line it cannot read is
// skipped: the log is Logos's own account, and one mangled line must not hide
// the rest of it.
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
		e, ok := parseEntry(strings.TrimSpace(sc.Text()))
		if !ok || e.At.Before(since) {
			continue
		}
		if project != "" && e.Project != "" && e.Project != oneWord(project) {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

var countWord = regexp.MustCompile(`^([a-z][\w-]*)=(\d+)$`)

// parseEntry reads one line Record wrote. Anything else — the header, a line
// edited into nonsense — is not an entry.
func parseEntry(line string) (Entry, bool) {
	var e Entry
	rest, ok := strings.CutPrefix(line, "- ")
	if !ok {
		return e, false
	}
	at, rest, _ := strings.Cut(rest, " ")
	sw, rest, _ := strings.Cut(rest, " ")
	t, err := time.Parse(time.RFC3339, at)
	if err != nil || sw == "" {
		return e, false
	}
	// The log is vault text, and what it says opens resume above the frame that
	// marks the rest as data (invariant 6). A switch Logos does not have is a
	// line Logos never wrote, and the free text is made one line again here,
	// not only by Record: bufio splits on \n alone, so a \r or U+2028 edited
	// into a line would otherwise reach resume as a line break of its own.
	if _, ok := known(Switch(sw)); !ok {
		return e, false
	}
	e.At, e.Switch = t, Switch(sw)
	if p, ok := strings.CutPrefix(rest, "project="); ok {
		e.Project, rest, _ = strings.Cut(p, " ")
		e.Project = oneWord(e.Project)
	}
	if rest == "started" {
		e.Started = true
		return e, true
	}
	if msg, ok := strings.CutPrefix(rest, "failed: "); ok {
		e.Failed = untrusted.Inline(msg)
		return e, true
	}
	if i := strings.Index(" "+rest, " skipped: "); i >= 0 {
		e.Skipped = untrusted.Inline((" " + rest)[i+len(" skipped: "):])
		rest = strings.TrimSpace(rest[:max(i-1, 0)])
	}
	if rest == "" {
		return e, e.Skipped != ""
	}
	for _, w := range strings.Fields(rest) {
		m := countWord.FindStringSubmatch(w)
		if m == nil {
			return e, false
		}
		n, _ := strconv.Atoi(m[2])
		if m[1] == "n" {
			e.N = n
		} else {
			e.Counts = append(e.Counts, Count{m[1], n})
		}
	}
	return e, true
}

// did is how resume words a count of one switch's actions.
func did(s Switch, n int) string {
	switch s {
	case Checkpoint:
		return fmt.Sprintf("checkpointed %d session%s", n, plural(n))
	case Review:
		return fmt.Sprintf("accepted %d memor%s from the review queue", n, map[bool]string{true: "y", false: "ies"}[n == 1])
	case Index:
		if n == 1 {
			return "rebuilt the missing index"
		}
		return fmt.Sprintf("rebuilt the missing index %d times", n)
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
	counts := map[Switch][]Count{}
	skipped := map[Switch][]string{}
	var failed []string
	var lastDream *Entry
	for i, e := range entries {
		if e.Switch == Dream {
			lastDream = &entries[i]
		}
		if e.Started {
			continue
		}
		if e.Failed != "" {
			failed = append(failed, fmt.Sprintf("%s failed (%s)", e.Switch, e.Failed))
			continue
		}
		if _, ok := count[e.Switch]; !ok {
			order = append(order, e.Switch)
		}
		count[e.Switch] += e.N
		counts[e.Switch] = addCounts(counts[e.Switch], e.Counts)
		if e.Skipped != "" && !contains(skipped[e.Switch], e.Skipped) {
			skipped[e.Switch] = append(skipped[e.Switch], e.Skipped)
		}
	}
	var parts []string
	for _, s := range order {
		if s == Dream {
			parts = append(parts, dreamDid(counts[s], skipped[s]))
		} else if count[s] > 0 {
			parts = append(parts, did(s, count[s]))
		}
	}
	parts = append(parts, failed...)
	// A dream that started and never wrote its result died somewhere nobody
	// was watching. Silence is what it would otherwise look like.
	if lastDream != nil && lastDream.Started && time.Since(lastDream.At) > dreamSilent {
		parts = append(parts, fmt.Sprintf("dream started %s and never reported back", lastDream.At.Local().Format("Mon 15:04")))
	}
	if len(parts) == 0 {
		return ""
	}
	return "auto: " + strings.Join(parts, ", ") + " · `logos auto status`"
}

// dreamSilent is how long a started dream may go without a result before
// resume calls it lost. A dream on a slow local model takes minutes, not hours.
const dreamSilent = 2 * time.Hour

func addCounts(into, add []Count) []Count {
next:
	for _, a := range add {
		for i := range into {
			if into[i].Name == a.Name {
				into[i].N += a.N
				continue next
			}
		}
		into = append(into, a)
	}
	return into
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// dreamWords is how each of dream's counts reads in a sentence.
var dreamWords = map[string]string{
	"merged":     "merged %d",
	"superseded": "superseded %d",
	"faded":      "%d fading",
	"insights":   "%d connection(s) to review",
}

// dreamDid words dream's runs. A skip is said as a skip, never as zeros: "merged
// 0" after a night with no model reads as a night with nothing to merge.
func dreamDid(cs []Count, skipped []string) string {
	var words []string
	for _, c := range cs {
		if c.N == 0 {
			continue // counted, so known to be none; a skip is said below
		}
		if f, ok := dreamWords[c.Name]; ok {
			words = append(words, fmt.Sprintf(f, c.N))
		} else {
			words = append(words, fmt.Sprintf("%s %d", c.Name, c.N))
		}
	}
	out := "dream"
	switch {
	case len(words) > 0:
		out += " " + strings.Join(words, ", ")
	case len(skipped) == 0:
		out += " ran and found nothing to change"
	}
	if len(skipped) > 0 {
		out += " (skipped: " + strings.Join(skipped, "; ") + ")"
	}
	return out
}
