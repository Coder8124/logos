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
// It is silent where nobody is reading: a hook (LOGOS_HOOK — hookCmd's own flag
// is not set yet when this runs), and the background runs logos launches of
// itself (`dream --auto`), would otherwise fill a host's log with a notice no
// one can act on. `activity record` and `activity notice` are what installed
// hooks run, so they are plumbing rather than a retired spelling.
func noteRenamed(w io.Writer, cmd string, args []string) {
	now, ok := renamed[cmd]
	if !ok || os.Getenv("LOGOS_HOOK") != "" || hasFlag(args, "--auto") {
		return
	}
	if cmd == "activity" && len(args) > 0 {
		switch args[0] {
		case "record", "notice":
			return
		case "on", "off", "status":
			// The switch moved to config, not to the log viewer: pointing
			// `activity off` at `watch --log` sends someone to read the log
			// they asked to stop.
			now = "logos config activity"
		}
	}
	fmt.Fprintf(w, "logos: `logos %s` is now `%s`; the old name stops working in a later release\n", cmd, now)
}

// surface maps a command line in the condensed spelling onto the verb and
// arguments main dispatches on, and names the form as the person typed it, so
// an error about the line can say `logos show --history` rather than a verb
// they never wrote. A line already in an old spelling, or in one that did not
// change, comes back as it was.
func surface(cmd string, args []string) (verb string, rest []string, form string) {
	form = "logos " + cmd
	switch cmd {
	case "search":
		// Only before the query: everything after it is the text searched
		// for, and a query about the flag is not a request for an answer.
		if len(args) > 0 && args[0] == "--answer" {
			return "ask", args[1:], form + " --answer"
		}
	case "memory":
		if len(args) > 0 && slices.Contains([]string{"recall", "dream", "loop"}, args[0]) {
			return args[0], args[1:], form + " " + args[0]
		}
	case "show":
		if flag, verb := firstForm(args, map[string]string{
			"--history":    "sessions",
			"--plans":      "plans",
			"--continuity": "continuity",
			"--projects":   "projects",
			"--graph":      "graph",
			"--since-last": "replay",
		}); verb != "" {
			return verb, positionals(args, flag), form + " " + flag
		}
	case "stats":
		if flag, verb := firstForm(args, map[string]string{
			"--usage":  "usage",
			"--memory": "reflect",
		}); verb != "" {
			return verb, positionals(args, flag), form + " " + flag
		}
		return "insights", args, form
	case "config":
		// A bare `config`, or a setting it does not have, stays "config" for
		// configCmd, which answers with the list of settings that exist.
		if len(args) > 0 && slices.Contains(configSettings, args[0]) {
			return args[0], args[1:], form + " " + args[0]
		}
	case "import":
		switch from := flagStr(args, "--from", ""); from {
		case "transcripts":
			return "ingest", dropFlag(args, "--from"), form + " --from " + from
		case "git":
			return "bootstrap", dropFlag(args, "--from"), form + " --from " + from
		}
	case "watch":
		if hasFlag(args, "--log") {
			return "activity", positionals(args, "--log"), form + " --log"
		}
	case "review":
		// Three queues wait for a person's yes. Memories are the default; the
		// other two are reached from the same verb so there is one place to
		// look, while each keeps the handler that knows its own answers.
		switch {
		case hasFlag(args, "--candidates"):
			return "ingest", append([]string{"review"}, positionals(args, "--candidates")...), form + " --candidates"
		case hasFlag(args, "--connections"):
			// Anything but accept or reject is the queue. dream with any other
			// first argument is a whole nightly pass, so `--connections --all`
			// must not reach it as a request to look.
			rest := positionals(args, "--connections")
			if len(rest) == 0 || !slices.Contains([]string{"review", "accept", "reject"}, rest[0]) {
				rest = append([]string{"review"}, rest...)
			}
			return "dream", rest, form + " --connections"
		}
	case "setup":
		if hasFlag(args, "--uninstall") {
			return "mcp", append([]string{"uninstall"}, positionals(args, "--uninstall")...), form + " --uninstall"
		}
	}
	return cmd, args, form
}

// firstForm is the form flag that comes first on the line, and its verb. By
// position, not by table order: `show --graph --history` asked for the graph,
// and the --history left over is the handler's to refuse.
func firstForm(args []string, forms map[string]string) (flag, verb string) {
	for _, a := range args {
		if v, ok := forms[a]; ok {
			return a, v
		}
	}
	return "", ""
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
