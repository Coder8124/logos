package gitstate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// #197: Logos reads git state from a hook, in whatever folder the user opened,
// and a folder unpacked from an archive brings its own .git with it. Several
// things in there name a program that a plain read runs:
//
//   - core.fsmonitor, run by status and diff to find changed files;
//   - filter.<name>.clean and .process, run by status and diff on every
//     changed file the attributes map to that filter;
//   - gpg.program, run by log to verify a signature when log.showSignature is
//     set — the commit only has to carry a signature, not a valid one;
//   - .git/hooks/post-index-change, run when status or diff writes back an
//     index whose stat data is stale, which after unpacking is all of it;
//   - any of the above in a submodule, whose config under .git/modules the
//     overrides here do not reach, so status and diff are given
//     --ignore-submodules=all and a changed submodule goes uncounted.
//
// Each was reproduced against git 2.54 before it was listed. textconv and
// external diff drivers were checked too, and diff --numstat runs neither.

// ErrUnsafe is returned when git cannot be told to leave the repository's
// programs alone, so it was not run.
var ErrUnsafe = errors.New("git: a filter in this repository cannot be overridden")

// waitDelay caps the wait for output after a timeout kills git: a child it
// started can hold the pipe open, and the kill is meant to end the wait.
const waitDelay = 500 * time.Millisecond

// SafeGit runs git in dir with the repository's own programs turned off,
// bounded by timeout end to end.
func SafeGit(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	safe, err := safeArgs(ctx, dir)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", append(append(safe, "-C", dir), args...)...)
	// Without optional locks, status and diff never write the index back, so
	// the hook that runs on an index write has nothing to run on.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	cmd.WaitDelay = waitDelay
	out, err := cmd.Output()
	return string(out), err
}

// safeArgs builds the -c overrides, placed before the subcommand.
func safeArgs(ctx context.Context, dir string) ([]string, error) {
	args := []string{
		"-c", "core.fsmonitor=false",
		"-c", "log.showSignature=false",
		"-c", "core.hooksPath=/dev/null",
	}
	// Reading config runs nothing, whatever the config says. It is bounded all
	// the same: a .git/config on a hung mount blocks the read like any other.
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "config", "--show-scope",
		"--name-only", "--get-regexp", `^filter\.`)
	cmd.WaitDelay = waitDelay
	out, err := cmd.Output()
	if err != nil {
		// Exit 1 is "no filter configured", the ordinary case.
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || ctx.Err() != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		scope, key, _ := strings.Cut(line, "\t")
		// The user's own filters are not the threat, and git-lfs keeps
		// filter.lfs in the global config: blanked, every LFS file with stale
		// stat data reads as modified. Only what the repository brought is.
		if scope != "local" && scope != "worktree" {
			continue
		}
		rest, found := strings.CutPrefix(key, "filter.")
		dot := strings.LastIndex(rest, ".")
		if !found || dot <= 0 || seen[rest[:dot]] {
			continue
		}
		name := rest[:dot]
		seen[name] = true
		// -c splits at the first =, so a driver whose name contains one
		// cannot be overridden; refuse rather than read with it live.
		if strings.Contains(name, "=") {
			return nil, ErrUnsafe
		}
		// An empty command is git's "no filter". required has to go too, or
		// git refuses the read of a file whose required filter did not run.
		for _, kv := range []string{".clean=", ".smudge=", ".process=", ".required=false"} {
			args = append(args, "-c", "filter."+name+kv)
		}
	}
	return args, nil
}
