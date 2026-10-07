// Command logos is the CLI front end. The same packages back the Wails app, so
// there is one engine rather than two.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/Coder8124/logos/internal/buildinfo"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
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

// start settles which vault this run uses before anything reads one.
//
// #95: inside a host, that host's own pin beats the machine pointer, so one
// session cannot restore from one disk and checkpoint to another. The old
// names are checked after, against that vault: checked first, a .brain in the
// pinned vault went unreported and ~/brain was named to a run that was about
// to use somewhere else entirely.
func start(stderr io.Writer, hosts []setup.Host) {
	adoptHostPin(hosts)
	warnOldNames(stderr)
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
	start(os.Stderr, setup.Hosts())
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	if cmd != "migrate" {
		migrateHint(os.Stderr)
	}
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
	case cmd == "migrate":
		err = migrateCmd(args)
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
		serveTools = toolSetFrom(args)
		err = runMCPServeHTTP(flagInt(args, "--port", 8137))
	case cmd == "mcp" && len(args) >= 1 && args[0] == "serve":
		serveTools = toolSetFrom(args)
		err = runMCPServe()
	case cmd == "bench" && len(args) >= 2 && args[0] == "memory" && hasFlag(args, "--qa"):
		err = runBenchQA(args[1], flagInt(args, "--n", 100), !hasFlag(args, "--vector"), flagInt(args, "--depth", 5))
	case cmd == "bench" && len(args) >= 2 && args[0] == "memory":
		err = runBench(args[1], flagInt(args, "--n", 100), !hasFlag(args, "--vector"))
	case cmd == "bench" && len(args) >= 1 && args[0] == "pipeline":
		err = runPipelineBench()
	case cmd == "graph":
		hops := flagInt(args, "--hops", 0)
		if hops == 0 {
			hops = 2 // 0 asks for the default, as --budget 0 does
		}
		err = runGraph(firstNonFlag(args), hops, hasFlag(args, "--similar"), hasFlag(args, "--list"))
	default:
		unknownCommand(cmd)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
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
// internal/vault, because every front end needs the same answer, and the old
// desktop app having its own copy is how it came to open a different vault
// than the CLI on the same machine.
func vaultPath() string { return vault.Path() }

// findProvider picks the first running local runtime. Cloud BYOK slots in here
// later by reading a configured base URL and key.
//
// LOGOS_RUNTIME overrides discovery with an explicit OpenAI-compatible base URL
// (LOGOS_RUNTIME_KEY for a bearer token). Discovery only probes localhost ports,
// so this is the only way to point logos at a runtime on another host, or at one
// on a non-standard port. LOGOS_RUNTIME=off is the no-runtime path on a machine
// that happens to have Ollama up.
func findProvider() (*provider.Provider, error) {
	if p := provider.Configured(); p != nil {
		fmt.Fprintf(os.Stderr, "· runtime %s (LOGOS_RUNTIME)\n", p.BaseURL)
		return p, nil
	}
	found := provider.Discover()
	if len(found) == 0 {
		if provider.Off() {
			return nil, fmt.Errorf("no model runtime: LOGOS_RUNTIME=off")
		}
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

// wasWere keeps the rescue receipts readable when exactly one thing was
// rescued, which is the commonest case — one failed write, one memory.
func wasWere(n int) string {
	if n == 1 {
		return "it was"
	}
	return "they were"
}
