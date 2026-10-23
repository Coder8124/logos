package main

import (
	"fmt"
	"io"
	"os"
	"slices"
)

// The command surface was condensed from about fifty verbs to twenty. The
// handlers did not move: surface translates the new spelling into the verb main
// has always dispatched on, so every behaviour, flag check and test of the old
// verb carries over unchanged, and a regression in the rename can only be a
// wrong row here rather than a handler that quietly lost a case.

// renamed is every retired spelling and what replaces it. The old spellings
// keep working for a release, saying so on stderr; stdout is left alone because
// a hook or a script may be reading it.
var renamed = map[string]string{
	"ask":        "logos search --answer",
	"recall":     "logos memory recall",
	"dream":      "logos memory dream",
	"loop":       "logos memory loop",
	"sessions":   "logos show --history",
	"plans":      "logos show --plans",
	"continuity": "logos show --continuity",
	"projects":   "logos show --projects",
	"graph":      "logos show --graph",
	"replay":     "logos show --since-last",
	"insights":   "logos stats",
	"usage":      "logos stats --usage",
	"reflect":    "logos stats --memory",
	"announce":   "logos config announce",
	"auto":       "logos config auto",
	"think":      "logos config think",
	"deny":       "logos config deny",
	"prompt":     "logos config prompt",
	"key":        "logos config key",
	"ingest":     "logos import --from transcripts",
	"bootstrap":  "logos import --from git",
	"activity":   "logos watch --log",
}

// configSettings are the verbs `logos config <setting>` reaches. activity is
// here for its on|off switch; reading the log is `watch --log`.
var configSettings = []string{"announce", "auto", "think", "deny", "prompt", "key", "activity"}

// noteRenamed tells a person typing a retired spelling what it is called now.
// It is silent where nobody is reading: a hook, and the background runs logos
// launches of itself (`dream --auto`), would otherwise fill a host's log with a
// notice no one can act on. `activity record` and `activity notice` are what
// installed hooks run, so they are plumbing rather than a retired spelling.
func noteRenamed(w io.Writer, cmd string, args []string) {
	now, ok := renamed[cmd]
	if !ok || inHook || os.Getenv("LOGOS_HOOK") != "" || hasFlag(args, "--auto") {
		return
	}
	if cmd == "activity" && len(args) > 0 && (args[0] == "record" || args[0] == "notice") {
		return
	}
	fmt.Fprintf(w, "logos: `logos %s` is now `%s`; the old name stops working in a later release\n", cmd, now)
}

// surface maps a command line in the condensed spelling onto the verb and
// arguments main dispatches on. A line already in an old spelling, or in one
// that did not change, comes back as it was.
func surface(cmd string, args []string) (string, []string) {
	switch cmd {
	case "search":
		if hasFlag(args, "--answer") {
			return "ask", without(args, "--answer")
		}
	case "memory":
		if len(args) > 0 && slices.Contains([]string{"recall", "dream", "loop"}, args[0]) {
			return args[0], args[1:]
		}
	case "show":
		for _, m := range []struct{ flag, verb string }{
			{"--history", "sessions"},
			{"--plans", "plans"},
			{"--continuity", "continuity"},
			{"--projects", "projects"},
			{"--graph", "graph"},
			{"--since-last", "replay"},
		} {
			if hasFlag(args, m.flag) {
				return m.verb, without(args, m.flag)
			}
		}
	case "stats":
		switch {
		case hasFlag(args, "--usage"):
			return "usage", without(args, "--usage")
		case hasFlag(args, "--memory"):
			return "reflect", without(args, "--memory")
		}
		return "insights", args
	case "config":
		// A bare `config`, or a setting it does not have, stays "config" for
		// configCmd, which answers with the list of settings that exist.
		if len(args) > 0 && slices.Contains(configSettings, args[0]) {
			return args[0], args[1:]
		}
	case "import":
		switch flagStr(args, "--from", "") {
		case "transcripts":
			return "ingest", dropFlag(args, "--from")
		case "git":
			return "bootstrap", dropFlag(args, "--from")
		}
	case "watch":
		if hasFlag(args, "--log") {
			return "activity", without(args, "--log")
		}
	case "review":
		// Three queues wait for a person's yes. Memories are the default; the
		// other two are reached from the same verb so there is one place to
		// look, while each keeps the handler that knows its own answers.
		switch {
		case hasFlag(args, "--candidates"):
			return "ingest", append([]string{"review"}, without(args, "--candidates")...)
		case hasFlag(args, "--connections"):
			rest := without(args, "--connections")
			if len(rest) == 0 {
				rest = []string{"review"}
			}
			return "dream", rest
		}
	case "setup":
		if hasFlag(args, "--uninstall") {
			return "mcp", append([]string{"uninstall"}, without(args, "--uninstall")...)
		}
	}
	return cmd, args
}

// configCmd is `logos config` with no setting, or one it does not have. Asked
// for the list, it answers on stdout; given a setting that does not exist, it
// is an error that names the ones that do.
func configCmd(args []string) error {
	if len(args) == 0 {
		commandHelp(os.Stdout, "config")
		return nil
	}
	return fmt.Errorf("logos config has no setting %q — it has %v", args[0], configSettings)
}

// without is args with every exact occurrence of a bare flag removed.
func without(args []string, flag string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != flag {
			out = append(out, a)
		}
	}
	return out
}
