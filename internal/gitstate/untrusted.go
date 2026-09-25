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
//   - post-index-change, run when diff writes back an index whose stat data
//     is stale, which after unpacking is all of it — from .git/hooks, and from
//     hook.<name>.command in the config, which core.hooksPath does not reach;
//   - lfs.extension.<name>.clean, which the user's own git-lfs filter reads
//     from the repository's config and runs;
//   - remote.<name>.uploadpack or core.sshCommand, run when a partial clone
//     fetches a missing blob on demand — which diff-index does for the old
//     side of a changed file, and which is a network call besides. The
//     repository can re-allow a transport with protocol.<name>.allow, which
//     git reads before protocol.allow;
//   - any of the above in a submodule, whose config under .git/modules the
//     overrides here do not reach, so status and diff are given
//     --ignore-submodules=all and a changed submodule goes uncounted.
//
// Each was reproduced against git 2.54 before it was listed. textconv and
// external diff drivers were checked too, and diff --numstat runs neither.

// ErrUnsafe is returned when git cannot be told to leave the repository's
// programs alone, so it was not run.
var ErrUnsafe = errors.New("git: a filter or hook in this repository cannot be overridden")

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
	// Without optional locks status never writes the index back. diff ignores
	// the setting, which is why diffStat uses diff-index, which never writes.
	// GIT_NO_LAZY_FETCH makes a partial clone's missing blob an error rather
	// than a fetch. A git older than 2.45 does not know the variable, and there
	// the protocol overrides safeArgs builds are what refuse the fetch.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1")
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
		"-c", "protocol.allow=never",
	}
	// Reading config runs nothing, whatever the config says. It is bounded all
	// the same: a .git/config on a hung mount blocks the read like any other.
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "config", "--show-scope",
		"--name-only", "--get-regexp", `^(filter|hook|lfs|protocol)\.`)
	cmd.WaitDelay = waitDelay
	out, err := cmd.Output()
	if err != nil {
		// Exit 1 is "nothing configured", the ordinary case.
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || ctx.Err() != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		scope, key, _ := strings.Cut(line, "\t")
		// protocol.<name>.allow outranks protocol.allow, so one set to always
		// re-opens that transport. Every one is closed, whatever its scope:
		// reading state never needs a transport, even one the user allowed.
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "protocol.") && strings.HasSuffix(lower, ".allow") && lower != "protocol.allow" {
			name := key[len("protocol.") : len(key)-len(".allow")]
			if strings.Contains(name, "=") {
				return nil, ErrUnsafe
			}
			args = append(args, "-c", "protocol."+name+".allow=never")
			continue
		}
		// The user's own filters and hooks are not the threat, and git-lfs
		// keeps filter.lfs in the global config: blanked, every LFS file with
		// stale stat data reads as modified. Only what the repository brought is.
		if scope != "local" && scope != "worktree" {
			continue
		}
		section, name := "", ""
		switch {
		// git prints a subsection as spelled, and [lfs "Extension.x"] is
		// the same extension to git-lfs, so the match ignores case.
		case strings.HasPrefix(strings.ToLower(key), "lfs.extension."):
			// An extension runs inside git-lfs's filter, so it is that
			// filter which is turned off — in this repository only.
			section, name = "filter", "lfs"
		case strings.HasPrefix(key, "lfs."):
			continue
		default:
			sec, rest, _ := strings.Cut(key, ".")
			dot := strings.LastIndex(rest, ".")
			if dot <= 0 {
				continue
			}
			section, name = sec, rest[:dot]
		}
		if seen[section+"."+name] {
			continue
		}
		seen[section+"."+name] = true
		// -c splits at the first =, so a driver or hook whose name contains
		// one cannot be overridden; refuse rather than read with it live.
		if strings.Contains(name, "=") {
			return nil, ErrUnsafe
		}
		// An empty command is git's "no filter". required has to go too, or
		// git refuses the read of a file whose required filter did not run.
		// A hook is switched off whole: an empty command is an error, not off.
		overrides := []string{".clean=", ".smudge=", ".process=", ".required=false"}
		if section == "hook" {
			overrides = []string{".enabled=false"}
		}
		for _, kv := range overrides {
			args = append(args, "-c", section+"."+name+kv)
		}
	}
	return args, nil
}
