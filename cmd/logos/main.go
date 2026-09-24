// Command logos is the CLI front end. The same packages back the Wails app, so
// there is one engine rather than two.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/buildinfo"
	"github.com/Coder8124/logos/internal/dream"
	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/mcpserver"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/setup"
	"github.com/Coder8124/logos/internal/vault"
)

// version is what `logos version` prints. It comes from internal/buildinfo so
// that this and the version the MCP handshake announces are the same string —
// they used to be two, and one of them was a literal nobody remembered to bump.
//
// "dev" is what a plain `go build` produces, and saying so is more useful than
// printing a number that is not tied to a release.
var version = buildinfo.Version

const (
	defaultEmbedModel = "nomic-embed-text"
	defaultChatModel  = "qwen3.6"
)

// The help is two surfaces, and the split is a product decision rather than a
// tidying one. logos grew about forty verbs, and printing all of them was an
// honest inventory that answered the wrong question: a first-time reader wants
// to know what this is *for*, and forty lines of episodic capture, voice and
// benchmarks say "a grab-bag" no matter what the first line claims.
//
// So the default is the three journeys the product is actually about — hand
// off, brief, intercept — plus the three commands that get you there. Nothing
// is hidden: `logos help all` is the old inventory, grouped, and every verb
// still works exactly as it did. This changes what help prints, not what logos
// does.

// helpShort is what `logos`, `logos help` and `logos --help` all print. It is
// deliberately one screen: the handoff is the centre of the product, and it is
// what a reader should be able to try in the next thirty seconds.
func helpShort(w io.Writer) {
	fmt.Fprint(w, `logos — local-first memory and continuity for AI agents

Agents forget the moment a session ends. logos is the memory they hand to one
another: one stops, the next picks up exactly where it left off.

THE HANDOFF — an agent finishes, and another continues
    logos note [project] <what you did>
                                      record progress; uncommitted until you checkpoint
    logos checkpoint [project] [--task ..] [--next ..] [--failed ..] [--agent <name>] [--handoff <agent>]
                                      commit where you stopped, as a note in the vault
    logos resume [project]            pick up where the last agent left off
                                      the project defaults to the directory you are in

THE BRIEF — what bears on the work, before the work starts
    logos context <task> [--project <p>] [--budget <n>]
                                      everything bearing on a task, budgeted (also an MCP tool)

THE INTERCEPT — the dead end nobody remembers recording
    logos tried <approach> [--project X]
                                      has this already been ruled out? ask before proposing
    logos tried <approach> --ruled-out <what happened> [--layer L] [--scope S]
                                      record one now, without waiting for a checkpoint

GETTING THERE
    logos setup [--vault DIR] [--host NAME] [--no-hosts] [--dry-run] [--yes] [--downgrade]
                                      connect logos to the AI agents on this machine
    logos mcp serve | mcp install     serve the memory to MCP hosts; wire the ones found
    logos mcp uninstall [--host NAME] take logos back out of the hosts; the vault is left alone
    logos doctor [--verbose] [--probe] [--integration] [--report]
                                      health of vault, index, hosts; --integration proves reach
                                      --report prints a paste-able bundle for a bug report
    logos update [--check]            check GitHub for a newer release, verify it, replace this binary

    LOGOS_VAULT points at the vault (default ~/logos)

`+"`logos help all`"+` lists the rest — memory, retrieval, benchmarks.
`)
}

// helpAll is the full inventory, grouped. It exists so that demoting the
// general surface does not amount to hiding it: everything logos has ever
// accepted is here, spelled the way you type it.
func helpAll(w io.Writer) {
	fmt.Fprintf(w, `logos — local-first memory and continuity for AI agents

CONTINUITY
    logos note [project] <what you did>
                                      record progress; uncommitted until you checkpoint
    logos checkpoint [project] [--task ..] [--state ..] [--next ..] [--decided ..]
                     [--verified ..] [--failed ..] [--blocker ..] [--ran ..]
                     [--question ..] [--file ..] [--agent <name>] [--handoff <agent>]
                                      commit where you stopped, as a note in the vault
                                      repeat --decided, --verified, --failed, --blocker,
                                      --ran, --question and --file to add more than one
    logos resume [project]            pick up where the last agent left off
                                      the project defaults to the directory you are in
    logos ingest [project] [--harness N] [--path FILE|ID] [--dry-run] [--all-projects]
                                      harvest other agents' transcripts into checkpoint candidates
                                      --path is a file, or for a txcript harness a session id
    logos ingest review [--promote <id> | --reject <id>]
                                      review candidates before they become checkpoints
    logos ingest status               candidates by tier, and how many can still be distilled
    logos ingest archive <dir>        copy the cited transcripts somewhere you keep them
    logos sessions [project]          checkpoint history for a project, and any abandoned ones
    logos plans [project]             plan-mode plans saved when ExitPlanMode is approved
    logos continuity                  vault-wide: which projects checkpoint, which have gone quiet
    logos bootstrap [project] [--dir DIR] [--dry-run] [--months N]
                                      seed a cold vault from this repo's git history
    logos context <task> [--project <p>] [--budget <n>]
                                      everything bearing on a task, budgeted (also an MCP tool)
    logos tried <approach> [--project X]
                                      has this already been ruled out? ask before proposing
    logos tried <approach> --ruled-out <what happened> [--layer L] [--scope S]
                                      record one now, without waiting for a checkpoint
    logos insights [project]          patterns already in the vault: a recurring blocker, a dormant memory
    logos usage [project] [--usd RATE]
                                      what the budget left out of context packs, and
                                      dead ends handed back before a retry
    logos usage off | on              stop or resume counting (LOGOS_USAGE=off for one process)
    logos why <file> [--limit N]      what was being decided when this file was touched
    logos projects | project <name>   auto-detected projects and their dossiers
    logos project-name [dir]          the project name for a directory, as the hooks compute it
    logos hook <cursor|codex> session-start
                                      what a host's session-start hook runs; prints the handoff as JSON
    logos plugin autoupdate [--notice]
                                      update the Claude Code plugin when it is older than this
                                      binary, at most once a day; --notice prints what it did, once
    logos project rename <old> <new> [--dry-run] [--merge]
                                      rename a project, carrying its history with it;
                                      --merge combines it into an existing project instead of refusing

MEMORY
    logos memory [add <fact>|forget <id>|log|history <id>|graph|diff]   persistent memory
    logos memory [health|consolidate|pin <id>|unpin <id>|exclude <id>]
                                      what it knows about itself, and what to keep or ignore
    logos memory log [--project P] [--n N]   what changed in what it knows, newest first
    logos activity [--project P] [--kind K] [--tool T] [--days N] [--json]
                                      every prompt, tool call and turn the host reported —
                                      recorded automatically, not by the model's choice
    logos activity --projects         which projects are being recorded
    logos activity [off|on]           stop or resume recording it, for this vault
    logos announce [on|quiet|off]     how loudly Logos reports its own work
    logos prompt                      the instructions agents are given (LOGOSPROMPT.md)
    logos demo [--fast]               ninety seconds showing what this is for, in a scratch vault
    logos memory diff [subject] [--since D] [--until D] [--days N]   what changed, instant & offline
    logos loop [list|add|done|drop]   list or manage open loops (commitments)
    logos graph [focus] [--hops N] [--similar]   memory graph around a note

RETRIEVAL
    logos search <query…>             retrieve only, no generation
    logos ask <question…>             retrieve and answer from the vault
    logos index [--watch]             sync vault into the cache and embed
    logos replay [--peek]             catch up on what changed since you were last here
    logos reflect                     descriptive stats over your memory (composition, growth, what it leans on)
    logos review [--all]              accept or reject quarantined memories
    logos dream [--date YYYY-MM-DD] [--phase nrem|rem] [--dry-run]
                                      nightly consolidation: replay, downscale, recombine
    logos dream review | accept|reject <id>
                                      review the connections REM proposed overnight
    logos think [off|low|medium|high]  how much the model reasons before answering

SETUP AND DIAGNOSTICS
    logos setup [--vault DIR] [--host NAME] [--no-hosts] [--dry-run] [--yes] [--downgrade]
                                      connect logos to the AI agents on this machine
    logos setup --print-config [--vault DIR] [--format json|toml]
                                      print the server block by hand, for any MCP client not listed above
    logos setup --config <path> [--vault DIR]
                                      merge logos into a config file at a location logos does not know by convention
    logos mcp serve                   serve the memory layer to MCP hosts (Claude Desktop, Cursor, your own apps)
    logos mcp serve --http [--port N] serve over a local WebSocket for the browser extension (ChatGPT/Claude.ai/
                                      Perplexity web UIs) — needs LOGOS_BRIDGE_ORIGIN set; never leaves localhost
    logos mcp install [--vault DIR] [--host NAME] [--dry-run] [--yes]
                                      register this logos with the MCP hosts found
    logos mcp uninstall [--host NAME] remove logos from the MCP hosts found; never touches the vault
    logos doctor [--verbose] [--probe] [--integration] [--report]
                                      health of vault, index, hosts; --verbose adds runtimes and tiers; --integration proves a host can reach it
    logos key set|rm <ref>            manage API keys in the macOS keychain
    logos update [--check]            check GitHub for a newer release, verify it, replace this binary
    logos version                     which build this is
    logos help [all]                  the three core journeys, or this list

BENCHMARKS
    logos bench continuity [list] [--only X] [--verbose] [--logos-only]
                                      the handoff + memory suite, against every system installed
    logos bench memory <file> | bench pipeline
                                      LongMemEval retrieval recall; the extract→recall loop

ENV
    LOGOS_VAULT     path to the vault (default ~/logos)
    LOGOS_MODEL     chat model (default %s)
    LOGOS_EMBED     embed model (default %s); "off" disables embeddings, search stays lexical
    LOGOS_RUNTIME   OpenAI-compatible base URL to use instead of auto-discovery
                    (LOGOS_RUNTIME_KEY for a bearer token)
`, defaultChatModel, defaultEmbedModel)
}

// commandHelp prints the lines of the full help that describe cmd, each with
// the explanation indented under it, and reports whether there were any.
func commandHelp(w io.Writer, cmd string) bool {
	if cmd == "" || strings.HasPrefix(cmd, "-") {
		return false
	}
	var all strings.Builder
	helpAll(&all)
	var out strings.Builder
	inEntry := false
	for _, line := range strings.Split(all.String(), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "logos "):
			inEntry = trimmed == "logos "+cmd || strings.HasPrefix(trimmed, "logos "+cmd+" ") ||
				strings.Contains(trimmed, "| "+cmd+" ")
		case trimmed == "" || !strings.HasPrefix(line, " "):
			inEntry = false
		}
		if inEntry {
			out.WriteString(line + "\n")
		}
	}
	if out.Len() == 0 {
		return false
	}
	fmt.Fprint(w, out.String())
	return true
}

// usage is the failure path — no arguments, or a verb nobody recognises. It
// prints the short help to stderr and exits non-zero, because a command line
// that could not be parsed is an error even though the text is identical to
// what `logos help` prints on success.
func usage() {
	helpShort(os.Stderr)
	os.Exit(2)
}

// unknownCommand reports what was actually wrong before falling back to the
// same help dump `usage()` gives a bare `logos` with no arguments — without
// this, a typo'd command name (`logos remember`) and no command at all
// printed the identical page, and only the exit code told them apart.
func unknownCommand(name string) {
	fmt.Fprintf(os.Stderr, "error: unknown command %q\n\n", name)
	usage()
}

func main() {
	carryOldNames(os.Stderr)
	// #95: inside a host, that host's own pin beats the machine pointer. Set
	// before anything reads a vault, so one session cannot restore from one
	// disk and checkpoint to another.
	adoptHostPin(setup.Hosts())
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	rest := strings.Join(args, " ")

	// Most commands never looked for --help: `update --help` replaced the
	// binary, `note --help` wrote a note reading "--help", `index --help`
	// rebuilt the index. Asking how a command works has to be free, so the
	// flag is answered here, before any command gets the chance to act.
	// setup, review and mcp install print their own fuller usage.
	if (hasFlag(args, "--help") || hasFlag(args, "-h")) && cmd != "setup" && cmd != "review" &&
		!(cmd == "mcp" && firstNonFlag(args) == "install") && commandHelp(os.Stdout, cmd) {
		return
	}

	if err := checkCommandFlags(cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	var err error
	switch {
	case cmd == "version", cmd == "--version", cmd == "-v":
		fmt.Printf("logos %s %s/%s %s\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	case cmd == "help", cmd == "--help", cmd == "-h":
		// Asked for, so it goes to stdout and exits 0 — the difference between
		// answering a question and reporting a bad command line. `help all`
		// takes the flag spelling too, so `logos --help all` is not a puzzle.
		if firstNonFlag(args) == "all" {
			helpAll(os.Stdout)
			break
		}
		helpShort(os.Stdout)
	case cmd == "setup":
		err = setupCmd(args)
	case cmd == "mcp" && len(args) >= 1 && args[0] == "install":
		err = mcpInstallCmd(args)
	case cmd == "mcp" && len(args) >= 1 && args[0] == "uninstall":
		err = mcpUninstallCmd(args)
	case cmd == "doctor":
		if hasFlag(args, "--report") {
			err = doctorReport()
			break
		}
		if hasFlag(args, "--integration") {
			err = doctorIntegration()
			break
		}
		err = doctor(hasFlag(args, "--probe"), hasFlag(args, "--verbose"))
	case cmd == "key":
		err = keyCmd(args)
	case cmd == "update":
		err = updateCmd(args)
	case cmd == "index":
		err = runIndex(hasFlag(args, "--watch"))
	case cmd == "search" && rest != "":
		err = search(rest)
	case cmd == "search":
		err = fmt.Errorf("usage: logos search <query>")
	case cmd == "ask" && rest != "":
		err = ask(rest)
	case cmd == "ask":
		err = fmt.Errorf("usage: logos ask <question>")
	case cmd == "context" && rest != "":
		err = runContext(args)
	case cmd == "context":
		err = runContext(args) // no args: its own usage message names the missing task
	case cmd == "note":
		err = runNote(args)
	case cmd == "checkpoint":
		err = runCheckpoint(args)
	case cmd == "resume":
		err = runResume(args)
	case cmd == "ingest":
		err = runIngest(args)
	case cmd == "bootstrap":
		err = runBootstrap(args)
	case cmd == "insights":
		err = runInsights(args)
	case cmd == "usage":
		err = runUsage(args)
	case cmd == "why":
		err = runWhy(args)
	case cmd == "tried":
		err = runTried(args)
	case cmd == "sessions":
		err = runSessionLog(args)
	case cmd == "plans":
		err = runPlans(args)
	case cmd == "continuity":
		err = runContinuity(args)
	case cmd == "think":
		err = runThink(rest)
	case cmd == "demo":
		err = runDemo(args)
	case cmd == "prompt":
		err = runPrompt(args)
	case cmd == "announce":
		err = runAnnounce(args)
	case cmd == "activity":
		err = runActivity(args)
	case cmd == "project-name":
		err = runProjectName(args)
	case cmd == "hook":
		err = hookCmd(args)
	case cmd == "plugin":
		err = pluginCmd(args)
	case cmd == "project" && len(args) > 0 && args[0] == "rename":
		err = runProjectRename(args[1:])
	case cmd == "review":
		err = runReview(args)
	case cmd == "dream":
		err = dreamCmd(args)
	case cmd == "replay":
		err = runReplay(hasFlag(args, "--peek"))
	case cmd == "reflect":
		err = runReflect()
	case cmd == "loop":
		err = commitmentCmd(args)
	case cmd == "memory":
		err = memoryCmd(args)
	case cmd == "projects":
		err = projectsCmd(args)
	case cmd == "project":
		err = projectCmd(args)
	case cmd == "mcp" && len(args) >= 1 && args[0] == "serve" && hasFlag(args, "--http"):
		err = runMCPServeHTTP(flagInt(args, "--port", 8137))
	case cmd == "mcp" && len(args) >= 1 && args[0] == "serve":
		err = runMCPServe()
	case cmd == "bench" && len(args) >= 2 && args[0] == "memory":
		err = runBench(args[1], flagInt(args, "--n", 100), !hasFlag(args, "--vector"))
	case cmd == "bench" && len(args) >= 1 && args[0] == "pipeline":
		err = runPipelineBench()
	case cmd == "bench" && len(args) >= 2 && args[0] == "continuity" && args[1] == "list":
		err = listBenchScenarios(flagStr(args, "--only", ""))
	case cmd == "bench" && len(args) >= 1 && args[0] == "continuity":
		err = runContinuityBench(args[1:])
	case cmd == "graph":
		hops := flagInt(args, "--hops", 0)
		if hops == 0 {
			hops = 2 // 0 asks for the default, as --budget 0 does
		}
		err = runGraph(firstNonFlag(args), hops, hasFlag(args, "--similar"))
	default:
		unknownCommand(cmd)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

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
	"replay":   {bare: []string{"--peek"}},
	"doctor":   {bare: []string{"--verbose", "--probe", "--integration", "--report"}},
	"resume":   {valued: []string{"--since"}, orDefault: []string{"--budget", "-b"}},
	"sessions": {valued: []string{"--close"}},
	"why":      {numeric: []string{"--limit", "-n"}},
	"usage":    {valued: []string{"--usd"}},
	"graph":    {orDefault: []string{"--hops"}, bare: []string{"--similar"}},
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

func argInt(args []string, pos, def int) int {
	if pos < len(args) {
		if v, err := strconv.Atoi(args[pos]); err == nil {
			return v
		}
	}
	return def
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// embedModel resolves the embedding model, honouring an explicit request for
// none. LOGOS_EMBED=off (also none/no/false/disabled) is the shell equivalent
// of logos.WithoutEmbedding(): paraphrase-tolerant search is off, everything
// else works. ok is false when embeddings are disabled, and callers must not
// pass the returned string to a runtime — it is empty.
func embedModel() (model string, ok bool) {
	switch v := env("LOGOS_EMBED", defaultEmbedModel); strings.ToLower(strings.TrimSpace(v)) {
	case "off", "none", "no", "false", "disabled":
		return "", false
	default:
		return v, true
	}
}

// vaultPath resolves where the vault lives. The rule itself lives in
// internal/vault, because the desktop app needs the same answer and having its
// own copy is how it came to open a different vault than the CLI on the same
// machine.
func vaultPath() string { return vault.Path() }

// findProvider picks the first running local runtime. Cloud BYOK slots in here
// later by reading a configured base URL and key.
//
// LOGOS_RUNTIME overrides discovery with an explicit OpenAI-compatible base URL
// (LOGOS_RUNTIME_KEY for a bearer token). Discovery only probes localhost ports,
// so this is the only way to point logos at a runtime on another host, or at one
// on a non-standard port — and the only way to exercise the no-runtime path on a
// machine that happens to have Ollama up.
func findProvider() (*provider.Provider, error) {
	if p := provider.Configured(); p != nil {
		fmt.Fprintf(os.Stderr, "· runtime %s (LOGOS_RUNTIME)\n", p.BaseURL)
		return p, nil
	}
	found := provider.Discover()
	if len(found) == 0 {
		return nil, fmt.Errorf("no local model runtime found — start Ollama, LM Studio, Jan or Msty, or set LOGOS_RUNTIME")
	}
	p := found[0]
	fmt.Fprintf(os.Stderr, "· %s at %s (%d models)\n", p.Provider.Name, p.Provider.BaseURL, len(p.Models))
	return p.Provider, nil
}

func openRouter() (*router.Router, error) {
	cfg, err := router.Load(vaultPath())
	if err != nil {
		return nil, err
	}
	return router.New(cfg, vaultPath())
}

// openRouterOptional is openRouter for the commands that do not actually need a
// model. A missing runtime comes back as (nil, nil) and the caller carries on
// degraded; a malformed config is still an error, because that is a mistake the
// user can fix and silently ignoring it would hide it.
//
// The distinction matters most for `mcp serve`. A host launches it in the
// background and shows the user a connection failure, not our message — so
// refusing to start over an absent model is how "logos has no embeddings here"
// becomes "logos is broken".
func openRouterOptional() (*router.Router, error) {
	rt, err := openRouter()
	if errors.Is(err, router.ErrNoRuntime) {
		return nil, nil
	}
	return rt, err
}

// missingVaultError is what every command says when the vault is not there.
//
// It used to say "set LOGOS_VAULT", which is the one instruction that cannot
// help either person who sees it: someone who has never run setup does not have
// a vault to point the variable at, and someone whose LOGOS_VAULT is a typo has
// already set it. Name the path that was tried, then the command that makes one.
// requireVault is the gate every command that reads or writes the vault goes
// through. `logos announce quiet` and `logos think medium` used to skip it and
// call straight into a store that creates its parent directory: pointed at a
// typo'd LOGOS_VAULT they built a half-vault out of nothing, reported success,
// and the setting the user had just changed was invisible to their real vault.
func requireVault() (string, error) {
	v := vaultPath()
	if _, err := os.Stat(v); err != nil {
		return "", missingVaultError(v)
	}
	return v, nil
}

func missingVaultError(v string) error {
	// The recorded vault being absent is not a first run. "Create one" there
	// makes an empty vault in the wrong place while the real one sits on a drive
	// that is not mounted.
	if os.Getenv("LOGOS_VAULT") == "" && v == vault.Pointer() {
		return fmt.Errorf("the vault recorded for this machine is not at %s — if it is on a drive, "+
			"reconnect it; to use a different vault, run `logos setup --vault <path>`", v)
	}
	return fmt.Errorf("vault not found at %s — run `logos setup` to create one, "+
		"or point LOGOS_VAULT at an existing vault", v)
}

func openIndex() (*index.Index, error) {
	v := vaultPath()
	if _, err := os.Stat(v); err != nil {
		return nil, missingVaultError(v)
	}
	return index.Open(v)
}

// openEvents opens the index and ensures the episodic tables exist alongside it.
// openEvents used to also ensure the ambient-capture tables existed alongside
// the index. That tier was cut in 0.3.0, so this is now
// exactly openIndex; kept as its own name because most callers below predate
// the cut and the rename churn buys nothing.
func openEvents() (*index.Index, error) {
	return openIndex()
}

func doctor(probe, verbose bool) error {
	// The product first, the model plumbing second. This used to be the other
	// way round — and in fact only ever reported the plumbing, so a vault that
	// did not exist and an index a week stale both passed silently.
	//
	// It also used to return an error when no runtime answered, which made the
	// one command a confused user reaches for refuse to run precisely when
	// something was wrong.
	rep := gatherHealth()
	fmt.Println("─── logos ───")
	// Width from the longest check name rather than a constant. "abandoned
	// sessions" is eighteen characters and used to push its own state out of the
	// column every other row lined up in, which reads as a rendering bug in the
	// one command someone runs when they already suspect something is wrong.
	w := 0
	for _, c := range rep.Checks {
		if len(c.Name) > w {
			w = len(c.Name)
		}
	}
	for _, c := range leadWith(rep.Checks, "vault", "agent hosts", "continuity") {
		fmt.Printf("  %-*s %s\n", w, c.Name, renderState(c.State))
		if c.Detail != "" {
			fmt.Printf("  %-*s   %s\n", w, "", c.Detail)
		}
		if c.Fix != "" {
			fmt.Printf("  %-*s   → %s\n", w, "", c.Fix)
		}
	}
	ok, warn, failed, unknown := rep.Counts()
	fmt.Printf("\n  %d ok · %d to do · %d failed · %d unchecked\n", ok, warn, failed, unknown)

	// Runtimes, tiers and the web bridge are for `ask`, the rollup and the
	// browser extension. No continuity tool uses them, and printed by default
	// they were most of the report and made logos look like it needed a model.
	if !verbose && !probe {
		fmt.Println("\nrun `logos doctor --verbose` for the web bridge, model runtimes and tiers")
		return doctorVerdict(failed)
	}

	if mcpserver.HasToken(vaultPath()) {
		fmt.Println("\nweb bridge: paired — `logos mcp serve --http` will reuse the existing token")
	} else {
		fmt.Println("\nweb bridge: not paired — `logos mcp serve --http` will mint a token on first run")
	}

	found := provider.Discover()
	if len(found) == 0 {
		// Not an error. Every continuity tool works without a model, and search
		// falls back to lexical; the report above already said so.
		fmt.Println("\nNo local model runtime — nothing above depends on one.")
		return doctorVerdict(failed)
	}
	fmt.Println("\n─── runtimes ───")
	for _, d := range found {
		fmt.Printf("%s — %s\n", d.Provider.Name, d.Provider.BaseURL)
		for _, m := range d.Models {
			fmt.Printf("    %s\n", m)
		}
	}

	cfg, err := router.Load(vaultPath())
	if err != nil {
		return err
	}
	rt, err := router.New(cfg, vaultPath())
	if err != nil {
		return err
	}

	fmt.Println("\n─── tiers ───")
	for _, line := range rt.Available() {
		fmt.Println(" ", line)
	}

	if !probe {
		fmt.Println("\nrun `logos doctor --probe` to verify each model actually loads")
		return doctorVerdict(failed)
	}

	// Listing a model proves nothing: a corrupt pull lists fine and fails on
	// load. Probing is what catches it before a rollup does at 3am.
	//
	// And a model that fails to load counts towards the verdict, or the probe
	// is the one check whose result nothing can act on: `failed` is totalled
	// before this loop runs, so `logos doctor --probe && deploy` used to print
	// FAILS TO LOAD in red and then exit 0 into the next command.
	fmt.Println("\n─── probe ───")
	for _, t := range []router.Tier{router.T1, router.T2} {
		model, err := rt.Model(t)
		if err != nil {
			fmt.Printf("  %s  %v\n", t, err)
			continue
		}
		line, broken := probeRow(t, model, rt.Probe(model))
		fmt.Println(line)
		if broken {
			failed++
		}
	}
	return doctorVerdict(failed)
}

// leadWith moves the named checks to the front, in that order, and keeps the
// rest as they were: what a coding-agent user runs doctor for is whether the
// vault is there, whether their agents are wired to it, and where the last
// session stopped.
func leadWith(checks []health.Check, names ...string) []health.Check {
	out := make([]health.Check, 0, len(checks))
	for _, n := range names {
		for _, c := range checks {
			if c.Name == n {
				out = append(out, c)
			}
		}
	}
	for _, c := range checks {
		if !slices.Contains(names, c.Name) {
			out = append(out, c)
		}
	}
	return out
}

// probeRow renders one probe result and says whether it counts as a failure.
// It is a function of its own so the verdict can be tested without a live model
// runtime: the bug it exists to stop — FAILS TO LOAD printed in the report while
// the command exits 0 — is only visible where the row and the count are decided
// together.
//
// A model that loads but ignores JSON schemas is not a failure. Every tier
// degrades to prose in that case, which is worse output, not a broken install.
func probeRow(t router.Tier, model string, cap router.Capability) (line string, failed bool) {
	switch {
	case !cap.Loads:
		return fmt.Sprintf("  %s  %-24s FAILS TO LOAD — %s", t, model, truncate(cap.Err, 70)), true
	case !cap.StructuredOutput:
		return fmt.Sprintf("  %s  %-24s loads, but ignores JSON schemas", t, model), false
	default:
		return fmt.Sprintf("  %s  %-24s ok, honours JSON schemas", t, model), false
	}
}

// doctorVerdict turns the report into an exit code. The rows already say what
// is wrong in words; this is for everything that reads the status instead — a
// pre-flight check, a CI step, a shell `&&`. An unchecked row is not a failure:
// doctor deliberately does not fail because no model runtime answered, since
// every continuity verb works without one.
func doctorVerdict(failed int) error {
	if failed == 0 {
		return nil
	}
	return fmt.Errorf("%d check(s) failed — see the report above", failed)
}

// doctorIntegration is the difference between "logos is installed" and "your
// agents can reach this vault". It is the same probe setup runs, exposed so it
// can be re-run after a host update or a config edit.
func doctorIntegration() error {
	vault := vaultPath()
	// The same description setup writes into every host config, so this check
	// launches what the hosts launch — under npx that is the `npx` command, not
	// the cached binary this process happens to be running from.
	srv, err := logosServer(vault)
	if err != nil {
		return err
	}

	self, err := selfPath()
	if err != nil {
		return err
	}

	// What the hosts have registered, not what this process happens to be
	// running from (#89). Someone who moved the binary onto their PATH, as the
	// end of setup told them to, left every host naming a file that is gone —
	// and this check said "Working", because it rebuilt the command from the
	// binary it found itself in. The question being asked is whether the
	// agents can reach the vault, and only their own entries can answer it.
	targets, unreadable := registeredTargets(vault, srv)
	failed := len(unreadable)
	for _, u := range unreadable {
		fmt.Printf("─── integration ───\n  host    %s\n  its registrations could not be read, so nothing here says whether it reaches this vault\n\n", u)
	}
	for i, t := range targets {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("─── integration ───\n  host    %s\n  binary  %s %s\n  vault   %s\n",
			t.host, t.srv.Bin, strings.Join(t.srv.Args, " "), t.vault)
		probeBin, probeArgs, note := probeTarget(self, t.srv)
		if note != "" {
			fmt.Printf("  note    %s\n", note)
		}
		fmt.Println()
		for _, c := range integrationChecks(probeBin, probeArgs, t.vault) {
			fmt.Printf("  %-12s %s\n", c.Name, renderState(c.State))
			if c.Detail != "" {
				fmt.Printf("  %-12s   %s\n", "", c.Detail)
			}
			if c.Fix != "" {
				fmt.Printf("  %-12s   → %s\n", "", c.Fix)
			}
			if c.State == health.Failed {
				failed++
			}
		}
	}
	if failed > 0 {
		// Named by count, not by "no host": one unreadable config among several
		// that read perfectly well sent the user to look for a permission
		// problem that was not there.
		if len(unreadable) > 0 {
			return fmt.Errorf("%d host(s) could not say what they have registered", len(unreadable))
		}
		return fmt.Errorf("integration is not working")
	}
	fmt.Println("\n  Working. The hosts launching these commands reach this vault.")
	return nil
}

// probe is one command to launch and the vault it is expected to reach, named
// by whoever registered it.
type probe struct {
	host  string
	srv   setup.Server
	vault string
}

// registeredTargets is the logos entry each detected host actually holds, and
// separately the hosts that could not be asked. A host with no logos in its
// config contributes nothing — it is not wired, so there is no wiring to check.
// A host whose config cannot be read is not that: it is the question going
// unanswered, so it is returned to be reported rather than dropped.
//
// Falling back to the command setup would write is what makes this check usable
// on a machine with no host registered yet: without it, `doctor --integration`
// on a fresh install would have nothing to probe and would report success by
// having asked nothing.
func registeredTargets(vault string, srv setup.Server) ([]probe, []string) {
	var out []probe
	var unreadable []string
	seen := map[string]bool{}
	for _, h := range detectHosts() {
		if h.List == nil || (h.Detect != nil && !h.Detect()) {
			continue
		}
		regs, err := h.List()
		if err != nil {
			// A host that cannot say what it has registered is not a host with
			// nothing registered. Swallowing this left the fallback probing
			// the command setup would write and the check closing "Working",
			// having failed to ask the only question it exists to ask.
			unreadable = append(unreadable, fmt.Sprintf("%s: %v", h.Name, err))
			continue
		}
		for _, r := range regs {
			if !strings.Contains(r.Command, "mcp serve") {
				continue
			}
			// Split on spaces, which is how the command was joined. A binary
			// path with a space in it is not reconstructed, and lands as a
			// command that fails to launch — visibly, which is the point.
			fields := strings.Fields(r.Command)
			v := r.Vault
			if v == "" {
				v = vault
			}
			// Keyed by the vault as well as the command: two hosts commonly
			// register the same binary against different vaults, and that
			// split is the thing this check exists to catch. Keyed by command
			// alone, the second host's vault was never probed.
			key := r.Command + "\x00" + v
			if len(fields) == 0 || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, probe{h.Name, setup.Server{Bin: fields[0], Args: fields[1:]}, v})
		}
	}
	if len(out) == 0 && len(unreadable) == 0 {
		return []probe{{"none registered — probing what setup would write", srv, vault}}, nil
	}
	return out, unreadable
}

// gatherHealth assembles what the checks need, tolerating every piece of it
// being missing. A vault that will not open, an index that is not there and a
// runtime that is not running each become Unknown rather than an early return —
// the point of the report is to work when things are broken.
func gatherHealth() health.Report {
	vault := vaultPath()
	in := health.Input{Vault: vault, EmbedModel: env("LOGOS_EMBED", defaultEmbedModel), Hosts: setup.Hosts(), Version: buildinfo.Version}
	if self, err := selfPath(); err == nil {
		in.Self = self
	}

	// Stat before opening, because index.Open creates <vault>/.logos and that
	// brings the vault itself into existence. Opening it here meant doctor made
	// the vault it was about to check and then pronounced it healthy — the
	// "does not exist" branch in checkVault could not fire from the CLI at all.
	// A mistyped LOGOS_VAULT, or doctor run before setup, produced a second
	// empty vault with a clean bill of health, which is exactly the "healthy
	// zero of everything" that internal/vault/path.go exists to prevent.
	//
	// A vault that exists but has never been indexed is a different case, and
	// index.Open creating .logos for that one is wanted.
	if _, err := os.Stat(vault); err == nil {
		if ix, err := index.Open(vault); err == nil {
			defer ix.Close()
			session.Init(ix.DB) // so the abandonment check reads a table rather than an error
			in.DB = ix.DB
		}
	}
	if found := provider.Discover(); len(found) > 0 {
		in.Runtime = found[0].Provider
	}

	return health.Run(in)
}

func renderState(s health.State) string {
	switch s {
	case health.OK:
		return "ok"
	case health.Warn:
		// Lower case and unshouted on purpose: this row is a chore waiting for
		// the user, and rendering it the way a broken index is rendered is what
		// made people stop reading the report.
		return "to do"
	case health.Failed:
		return "FAILED"
	default:
		// Spelled out, because the whole point is that this is not "fine".
		return "unchecked"
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func keyCmd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: logos key set|rm <ref>")
	}
	ref := args[1]

	switch args[0] {
	case "set":
		fmt.Fprintf(os.Stderr, "paste key for %q (input is not echoed to the terminal history): ", ref)
		var secret string
		if _, err := fmt.Scanln(&secret); err != nil {
			return err
		}
		if err := router.SetKey(ref, secret); err != nil {
			return err
		}
		fmt.Println("stored in keychain")
		return nil
	case "rm":
		return router.DeleteKey(ref)
	}
	return fmt.Errorf("usage: logos key set|rm <ref>")
}

func runIndex(watch bool) error {
	ix, err := openIndex()
	if err != nil {
		return err
	}
	defer ix.Close()

	// A vault someone put under git must never be offered .logos/ to commit —
	// it is a rebuildable cache, and two people sharing a vault over git would
	// otherwise fight a merge conflict in a SQLite file on every pull — nor the
	// activity log, which is every command and file path a host reported. Runs
	// every time and reports only the run that actually changed something, so
	// `logos index` calling this on every invocation never turns into noise.
	if wrote, err := vault.EnsureGitignore(ix.Vault); err != nil {
		fmt.Fprintln(os.Stderr, "· could not update .gitignore:", err)
	} else if wrote {
		fmt.Println("· .gitignore now keeps .logos/ and activity/ out of git")
	}

	// Sync is pure file reading — it needs no model, and it is what keeps the
	// FTS table current. Only the embedding passes need a provider.
	//
	// Requiring one here left a hole in the middle of the no-runtime story:
	// lexical search worked, but the command that refreshes what it searches did
	// not, so editing a note on a machine without Ollama meant the change was
	// invisible until a model appeared. Worse, `logos checkpoint` tells the user
	// to run exactly this command.
	embed, embedOK := embedModel()
	p, perr := findProvider()
	switch {
	case !embedOK:
		fmt.Fprintln(os.Stderr,
			"· embeddings off (LOGOS_EMBED) — indexing text only")
		p = nil // the pass below keys the embedding work off a nil provider
	case perr != nil:
		fmt.Fprintln(os.Stderr,
			"· no model runtime — indexing text only; run this again with Ollama up to add embeddings")
	}

	pass := func() error {
		rep, err := ix.Sync()
		if err != nil {
			return err
		}
		notes, _ := ix.NoteCount()
		edges, _ := ix.EdgeCount()

		// Working notes come back before anything that needs a model, because
		// restoring them needs nothing but the file — and this is the command a
		// user runs after deleting the index, which is precisely when they are
		// gone. Announced when there were any: a rebuild that silently recovered
		// in-flight work is indistinguishable from one that lost it.
		restoredNotes, rescuedNotes, err := ix.SyncNotes()
		if err != nil {
			fmt.Fprintln(os.Stderr, "· could not restore working notes:", err)
		}
		if restoredNotes > 0 {
			fmt.Printf("restored %d uncommitted working %s\n", restoredNotes, plural(restoredNotes, "note"))
		}
		// The other direction, and said out loud for the reason the rescued
		// proposals are: these were in the cache alone because a write to the
		// vault failed earlier, and the user was told that once, by a process
		// that has since exited. Silence here would make this run look like an
		// ordinary one while it repaired real data loss.
		if rescuedNotes > 0 {
			fmt.Printf("wrote %d working %s to the vault — %s only in the index\n",
				rescuedNotes, plural(rescuedNotes, "note"), wasWere(rescuedNotes))
		}

		// Memories and the review queue come back with or without a model.
		// Import needs a provider only to re-embed, and passing a nil one skips
		// exactly that — so this used to sit behind the `p == nil` return
		// below, which meant a rebuild on a machine with no runtime restored
		// the notes and left every remembered fact out of the cache until some
		// later run happened to have Ollama up. "Delete the index, lose
		// nothing" cannot depend on a model being reachable.
		mems, rescuedMems, err := ix.SyncMemories(p, embed)
		if err != nil {
			return err
		}
		// Same again for memories, and this is the count the bug was about: a
		// memory stranded in the cache used to be reaped here as a line the
		// user had deleted by hand, and reported under `-0`.
		if rescuedMems > 0 {
			fmt.Printf("wrote %d memor%s to the vault — %s only in the index\n",
				rescuedMems, pluralY(rescuedMems), wasWere(rescuedMems))
		}

		// The review queue, after the memories, so an accepted proposal is
		// already an active memory before the queue is consulted about its id.
		if queued, rescued, err := ix.SyncPending(); err != nil {
			fmt.Fprintln(os.Stderr, "· could not restore the review queue:", err)
		} else {
			if queued > 0 {
				fmt.Printf("restored %d memor%s awaiting review — run `logos review`\n",
					queued, pluralY(queued))
			}
			// Said out loud because it is a repair the user did not ask for and
			// would otherwise never know happened — and because it means their
			// queue was, until this run, one `rm -rf .logos` from gone.
			if rescued > 0 {
				fmt.Printf("wrote %d memor%s awaiting review to the vault — they were only in the index\n",
					rescued, pluralY(rescued))
			}
		}

		// The timeline, after both. Announced because the alternative — a silent
		// repair — is how the old failure hid: `logos memory log` answered
		// confidently after a rebuild, with dates invented on the spot, and
		// nothing on stdout ever said the history had been touched.
		if events, err := ix.SyncLog(); err != nil {
			fmt.Fprintln(os.Stderr, "· could not restore the memory timeline:", err)
		} else if events > 0 {
			fmt.Printf("restored %d memory %s — run `logos memory log`\n", events, plural(events, "event"))
		}

		// Open loops, which need no model either. Announced for the reason the
		// working notes are: an empty `logos loop` after a rebuild reads as a
		// list the user finished, not one the rebuild threw away. The count is
		// every loop put back, closed ones included — they are what stops a
		// dismissed commitment being extracted and surfaced all over again.
		if loops, err := ix.SyncLoops(); err != nil {
			fmt.Fprintln(os.Stderr, "· could not restore open loops:", err)
		} else if loops > 0 {
			fmt.Printf("restored %d tracked %s — run `logos loop`\n", loops, plural(loops, "loop"))
		}

		// Dreamed insights, after the memories they cite. Announced for the
		// reason the rest are: an empty `logos dream review` after a rebuild
		// reads as a queue the user has already been through, not one the
		// rebuild threw away. The count is every insight put back, reviewed
		// ones included — they are what stops a rejected connection being
		// proposed all over again.
		if seen, err := ix.SyncInsights(); err != nil {
			fmt.Fprintln(os.Stderr, "· could not restore dreamed insights:", err)
		} else if seen > 0 {
			// Rejections are restored too — they are the record of what the user
			// already refused. Only point at the review command when there is
			// actually something waiting behind it.
			line := fmt.Sprintf("restored %d dreamed %s", seen, plural(seen, "insight"))
			if n, err := dream.PendingCount(ix.DB); err == nil && n > 0 {
				line += " — run `logos dream review`"
			}
			fmt.Println(line)
		}

		if p == nil {
			fmt.Printf("+%d ~%d -%d =%d · %d notes, %d edges, %d memories · lexical only\n",
				rep.Added, rep.Updated, rep.Removed, rep.Unchanged, notes, edges, mems)
			return nil
		}

		embedded, err := ix.EmbedPending(p, embed, 32)
		if err != nil {
			return err
		}
		fmt.Printf("+%d ~%d -%d =%d · embedded %d · %d notes, %d edges, %d memories\n",
			rep.Added, rep.Updated, rep.Removed, rep.Unchanged, embedded, notes, edges, mems)
		return nil
	}

	if err := pass(); err != nil || !watch {
		return err
	}

	fmt.Printf("watching %s …\n", ix.Vault)
	// Poll rather than fsnotify: the vault is small, a 2s tick is imperceptible,
	// and it sidesteps the editor-save event storms that make watchers fire
	// three times per file.
	for range time.Tick(2 * time.Second) {
		if err := pass(); err != nil {
			fmt.Fprintln(os.Stderr, "· sync error:", err)
		}
	}
	return nil
}

func search(query string) error {
	ix, err := openIndex()
	if err != nil {
		return err
	}
	defer ix.Close()

	// No runtime is not a failure: FTS5 is in the index either way, so fall back
	// to the lexical arm alone. Exact terms — names, error codes, IDs — are found
	// as well as they ever were; only paraphrase suffers.
	var hits []index.Hit
	if model, ok := embedModel(); !ok {
		fmt.Fprintln(os.Stderr, "· embeddings off (LOGOS_EMBED) — searching lexically")
		hits, err = ix.LexicalSearch(query, 8)
	} else if p, perr := findProvider(); perr == nil {
		hits, err = ix.HybridSearch(p, model, query, 8)
	} else {
		fmt.Fprintln(os.Stderr, "· no model runtime — searching lexically")
		hits, err = ix.LexicalSearch(query, 8)
	}
	if err != nil {
		return err
	}
	// Zero hits printed nothing at all, which reads the same as a crash: a
	// first-time user searching for a typo could not tell whether the command
	// had worked. `logos ask` already says so in words; match it.
	if len(hits) == 0 {
		fmt.Printf("Nothing in the vault matches %q yet.\n", query)
		return nil
	}
	for _, h := range hits {
		fmt.Printf("%.3f  %-28s %s\n", h.Score, h.Slug, h.Title)
	}
	return nil
}

func ask(question string) error {
	ix, err := openIndex()
	if err != nil {
		return err
	}
	defer ix.Close()

	p, err := findProvider()
	if err != nil {
		return err
	}

	// ask still needs a chat model to synthesise the answer; an empty embed
	// model (LOGOS_EMBED=off) only sends retrieval down the lexical arm inside
	// HybridSearch rather than 404ing "off" at the runtime.
	model, ok := embedModel()
	if !ok {
		fmt.Fprintln(os.Stderr, "· embeddings off (LOGOS_EMBED) — retrieving lexically")
	}
	answer, hits, err := ix.Ask(p, model,
		env("LOGOS_MODEL", defaultChatModel),
		question, 6, 6000)
	if err != nil {
		return err
	}

	fmt.Printf("\n%s\n\n", strings.TrimSpace(answer))
	fmt.Println("─── context ───")
	for _, h := range hits {
		if h.Via != "" {
			fmt.Printf("  %-28s via %s\n", h.Slug, h.Via)
		} else {
			fmt.Printf("  %-28s %.3f\n", h.Slug, h.Score)
		}
	}
	return nil
}

// wasWere keeps the rescue receipts readable when exactly one thing was
// rescued, which is the commonest case — one failed write, one memory.
func wasWere(n int) string {
	if n == 1 {
		return "it was"
	}
	return "they were"
}
