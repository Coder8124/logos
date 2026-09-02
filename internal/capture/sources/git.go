package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Coder8124/brain/internal/event"
)

// CommitsSince returns commits authored after ts.
//
// The highest signal-per-row source available: a commit is an explicit,
// self-described unit of work the user already wrote. Scoped to the current
// user's own commits — pulling a colleague's merged work into your timeline
// would be wrong.
func CommitsSince(repo string, since int64) ([]event.Event, error) {
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return nil, nil
	}

	emailOut, err := exec.Command("git", "-C", repo, "config", "user.email").Output()
	if err != nil {
		return nil, nil
	}
	email := strings.TrimSpace(string(emailOut))

	out, err := exec.Command("git", "-C", repo, "log", "--all", "--no-merges",
		"--since="+strconv.FormatInt(since, 10),
		"--author="+email,
		"--pretty=format:%ct\x1f%s").Output()
	if err != nil {
		return nil, nil
	}

	repoName := filepath.Base(repo)
	var events []event.Event

	for _, line := range strings.Split(string(out), "\n") {
		tsStr, subject, ok := strings.Cut(line, "\x1f")
		if !ok {
			continue
		}
		ts, err := strconv.ParseInt(strings.TrimSpace(tsStr), 10, 64)
		// --since is inclusive-ish and drifts, so enforce the bound here or
		// repeated polls re-emit the same commit.
		if err != nil || ts <= since {
			continue
		}
		events = append(events, event.Event{
			TS: ts, Kind: event.Commit, App: "git", Title: subject, Path: repoName,
		})
	}
	return events, nil
}
