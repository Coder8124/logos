package gitstate

import (
	"os/exec"
	"strings"
)

// #197: Logos reads git state from a hook, in whatever folder the user opened,
// and a folder unpacked from an archive brings its own .git/config. Several of
// its settings name a program that a plain read runs:
//
//   - core.fsmonitor, run by status and diff to find changed files;
//   - filter.<name>.clean and .process, run by status and diff on every
//     changed file the attributes map to that filter;
//   - gpg.program, run by log to verify a signature when log.showSignature is
//     set — the commit only has to carry a signature, not a valid one.
//
// Each was confirmed against git 2.54 before it was listed. textconv and
// external diff drivers were checked too, and diff --numstat runs neither.
// Nothing Logos reads needs any of these programs, so every read turns them
// off. status and diff also pass --ignore-submodules=all: they would recurse
// into each submodule, whose own config under .git/modules can name filters
// these overrides do not know about. A changed submodule goes uncounted, which
// is the price.

// SafeArgs returns the -c overrides that stop git running programs the
// repository at dir configures, for placing before the subcommand. ok is false
// when an override cannot be written, and the caller must then not run git.
func SafeArgs(dir string) (args []string, ok bool) {
	args = []string{"-c", "core.fsmonitor=false", "-c", "log.showSignature=false"}
	// Reading config runs nothing, whatever the config says.
	out, err := exec.Command("git", "-C", dir, "config", "--name-only", "--get-regexp", `^filter\.`).Output()
	if err != nil {
		// Exit 1 is "no filter configured", the ordinary case.
		if e, isExit := err.(*exec.ExitError); !isExit || e.ExitCode() != 1 {
			return nil, false
		}
	}
	seen := map[string]bool{}
	for _, key := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		rest, found := strings.CutPrefix(key, "filter.")
		dot := strings.LastIndex(rest, ".")
		if !found || dot <= 0 {
			continue
		}
		name := rest[:dot]
		if seen[name] {
			continue
		}
		seen[name] = true
		// -c splits at the first =, so a driver whose name contains one
		// cannot be overridden; refuse rather than read with it live.
		if strings.Contains(name, "=") {
			return nil, false
		}
		// An empty command is git's "no filter". required has to go too, or
		// git refuses the read of a file whose required filter did not run.
		for _, kv := range []string{".clean=", ".smudge=", ".process=", ".required=false"} {
			args = append(args, "-c", "filter."+name+kv)
		}
	}
	return args, true
}
