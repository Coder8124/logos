package health

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/session"
)

// pluralWord is the ordinary -s pluraliser. The package's own plural() is the
// y/ies one, which memories need and notes do not.
func pluralWord(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// pluralS is the plain -s plural; plural() above is the y/ies one.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func newestMarkdown(dir string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner should not fail the whole check
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, err
}

// latestCheckpoint reads the most recent checkpoint across every project, off
// disk rather than from the index — the file is the record, and this check must
// work on a vault that has never been indexed.
func latestCheckpoint(vault string) (ts time.Time, project, agent string, err error) {
	// Walked, not listed. Checkpoints live at sessions/<project>/<id>.md, so
	// reading only the top level of sessions/ reports "no checkpoints yet" on a
	// vault full of them — which would make this check quietly useless in
	// exactly the case it exists to report on.
	dir := filepath.Join(vault, session.CheckpointDir)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return time.Time{}, "", "", nil
	}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		// Not every .md under sessions/ is a checkpoint. uncommitted.md holds
		// working notes and is rewritten on every note_progress, so it is almost
		// always the newest file here — which made this check answer "last
		// checkpoint 5 hours ago" for a vault whose last actual checkpoint was
		// days old. That is the precise failure the continuity check exists to
		// catch, reported as its own opposite.
		if err != nil || d.IsDir() || !session.IsCheckpointFile(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(ts) {
			ts = info.ModTime()
			project, agent = describeCheckpoint(path)
		}
		return nil
	})
	if err != nil {
		return time.Time{}, "", "", err
	}
	return ts, project, agent, nil
}

// describeCheckpoint pulls the project and agent out of a checkpoint's
// frontmatter. Best effort: a checkpoint that does not parse still counts as a
// checkpoint, it just cannot name itself.
func describeCheckpoint(path string) (project, agent string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "project:"); ok {
			project = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, "agent:"); ok {
			agent = strings.TrimSpace(v)
		}
		if line == "---" && project != "" {
			break
		}
	}
	return project, agent
}

// roughly renders a duration the way a person would say it.
func roughly(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return count(int(d.Minutes()), "minute")
	case d < 48*time.Hour:
		return count(int(d.Hours()), "hour")
	default:
		return count(int(d.Hours()/24), "day")
	}
}

// count saves the "1 hours" that makes a tool feel unfinished.
func count(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// shellArg makes a path safe to paste into a shell as one argument. A Fix is a
// command the user copies, and a vault under "~/My Drive" pasted bare is two
// arguments that set up the wrong directory. Single quotes, because inside them
// the shell expands nothing — a path with a "$" in it stays that path.
func shellArg(s string) string {
	plain := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-~+,:@%=", r)) {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
