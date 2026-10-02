package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

func flagInt(args []string, name string, def int) int {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				return v
			}
		}
	}
	return def
}

func joinArgs(a []string) string { return strings.Join(a, " ") }

func parseID(args []string) int64 {
	if len(args) >= 2 {
		var id int64
		fmt.Sscan(args[1], &id)
		return id
	}
	return 0
}

func firstNonFlag(args []string) string {
	for i := 0; i < len(args); i++ {
		if len(args[i]) >= 2 && args[i][:2] == "--" {
			i++ // skip a flag's value too
			continue
		}
		return args[i]
	}
	return ""
}

// isFlagToken reports whether a word is another flag rather than a value, so a
// flag given with nothing after it falls back to its default instead of eating
// the next one. `--project --kind decision` recorded the project as "--kind"
// and said nothing; invariant 4 says a missing value is reported as missing.
//
// A lone "-" is a value: it is the conventional name for stdin.
func isFlagToken(a string) bool {
	return strings.HasPrefix(a, "-") && a != "-"
}

// flagSpec is every flag one command understands. Anything else that starts
// with -- is refused by name before the command runs: `note --agent A` filed
// the note under a project called "agent", `memory add --kind fact` stored the
// flag inside the fact, and `tried x --bogus` answered "nothing rules this out"
// — each with a success message. A single dash is left alone, because "-" is
// stdin and a note or project may legitimately start with one.
type flagSpec struct {
	valued  []string // take the next word as their value
	numeric []string // valued, and a value that is given must be a positive whole number
	// orDefault is numeric, except that 0 is accepted and asks for the default.
	// Refusing it read as a broken command: --budget 0 is how a script says
	// "whatever you normally use", and a pack with no budget is no pack at all.
	orDefault []string
	bare      []string
}

// commandFlags covers the commands whose flags were parsed by picking out the
// known ones and ignoring the rest. Commands with their own strict parser
// (checkpoint, setup, update, mcp install) are not listed.
var commandFlags = map[string]flagSpec{
	"version":  {},
	"note":     {},
	"reflect":  {},
	"index":    {bare: []string{"--watch"}},
	"migrate":  {bare: []string{"--dry-run", "--yes", "-y"}},
	"replay":   {bare: []string{"--peek"}},
	"doctor":   {bare: []string{"--verbose", "--probe", "--integration", "--report"}},
	"resume":   {valued: []string{"--since"}, orDefault: []string{"--budget", "-b"}},
	"sessions": {valued: []string{"--close"}},
	"why":      {numeric: []string{"--limit", "-n"}},
	"usage":    {valued: []string{"--usd"}},
	"graph":    {orDefault: []string{"--hops"}, bare: []string{"--similar", "--list"}},
	"tried": {valued: []string{"--project", "--ruled-out", "--layer", "--scope", "--degree",
		"--action", "--instead"}},
	"context": {valued: []string{"--project", "-p", "--since", "--pin", "--exclude", "--unpin"},
		orDefault: []string{"--budget", "-b"}, bare: []string{"--rules"}},
}

// checkCommandFlags refuses a flag cmd does not know, or a number it cannot
// use. A command not in commandFlags is not checked here.
func checkCommandFlags(cmd string, args []string) error {
	spec, ok := commandFlags[cmd]
	if !ok {
		return nil
	}
	return checkFlags("logos "+cmd, args, spec)
}

func checkFlags(what string, args []string, spec flagSpec) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case slices.Contains(spec.bare, a):
		case slices.Contains(spec.valued, a):
			if i+1 < len(args) && !isFlagToken(args[i+1]) {
				i++
			}
		case slices.Contains(spec.numeric, a):
			// "-5" is a value the user typed, not a flag, so it is taken and
			// judged here rather than skipped as #116's missing value.
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				if n, err := strconv.Atoi(args[i]); err != nil || n <= 0 {
					return fmt.Errorf("%s needs a positive whole number, not %q", a, args[i])
				}
			}
		case slices.Contains(spec.orDefault, a):
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				if n, err := strconv.Atoi(args[i]); err != nil || n < 0 {
					return fmt.Errorf("%s needs a whole number, or 0 for the default, not %q", a, args[i])
				}
			}
		case strings.HasPrefix(a, "--"):
			known := slices.Concat(spec.valued, spec.numeric, spec.orDefault, spec.bare)
			if len(known) == 0 {
				return fmt.Errorf("unknown flag %q — %s takes no flags; nothing was done", a, what)
			}
			return fmt.Errorf("unknown flag %q — %s takes %s; nothing was done", a, what, strings.Join(known, ", "))
		}
	}
	return nil
}

func flagStr(args []string, name, def string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) && !isFlagToken(args[i+1]) {
			return args[i+1]
		}
	}
	return def
}

// dropFlag removes a value flag and its value from an argument list, so a
// command whose remaining words are free text can take flags at all.
//
// Without it, `memory add <fact> --project kestrel` stores the flag as part of
// the fact — the memory reads as though it were scoped and is in fact scoped to
// nothing, which is worse than the flag simply not existing.
func dropFlag(args []string, name string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == name {
			// Skip the value too, unless the flag was given last with
			// nothing after it, or what follows is another flag — in which
			// case there is no value to skip and dropping a word would take
			// the next flag out of the line with it.
			if i+1 < len(args) && !isFlagToken(args[i+1]) {
				i++
			}
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// flagStrs collects a flag that may be given more than once, and also accepts a
// comma-separated list, so `--host claude-code --host codex` and
// `--host claude-code,codex` both work. Whichever a user reaches for first is
// the one that should have worked.
func flagStrs(args []string, name string) []string {
	var out []string
	for i, a := range args {
		if a != name || i+1 >= len(args) || isFlagToken(args[i+1]) {
			continue
		}
		for _, part := range strings.Split(args[i+1], ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}
