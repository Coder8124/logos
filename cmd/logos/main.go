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

	"github.com/Coder8124/logos/internal/auto"
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
		if hasFlag(args, "--egress") {
			err = doctorEgress()
			break
		}
		err = doctor(hasFlag(args, "--probe"), hasFlag(args, "--verbose"))
	case cmd == "key":
		err = keyCmd(args)
	case cmd == "update":
		err = updateCmd(args)
	case cmd == "index":
		if hasFlag(args, "--auto") {
			err = runIndexAuto()
		} else {
			err = runIndex(hasFlag(args, "--watch"))
		}
	case cmd == "search" && rest != "":
		err = search(rest)
	case cmd == "search":
		err = fmt.Errorf("usage: logos search <query>")
	case cmd == "recall":
		err = recallCmd(args)
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
	case cmd == "import":
		err = runImport(args)
	case cmd == "insights":
		err = runInsights(args)
	case cmd == "usage":
		err = runUsage(args)
	case cmd == "show":
		err = runShow(args)
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
	case cmd == "auto":
		err = runAuto(args)
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

// openIndex opens the vault's index for a command that reads it. An index no
// Sync has finished on is said on stderr (stdout may be JSON): a deleted index
// otherwise answers "nothing matches" and "no memories" with full confidence,
// which reads as a vault that lost everything rather than a cache to rebuild.
func openIndex() (*index.Index, error) {
	ix, err := openIndexQuiet()
	if err != nil {
		return nil, err
	}
	checkIndex(ix)
	return ix, nil
}

// checkIndex says, or with the index switch on repairs, an index no Sync has
// finished on. On stderr, which over MCP stdio is the host's log and never the
// protocol stream.
func checkIndex(ix *index.Index) {
	switch synced, err := ix.Synced(); {
	case err != nil:
		fmt.Fprintf(os.Stderr, "logos: could not tell whether the index was ever built: %v\n", err)
	case !synced && auto.On(ix.Vault, auto.Index) && !inHook && os.Getenv("LOGOS_HOOK") == "":
		rebuildIndex(ix)
	case !synced && auto.On(ix.Vault, auto.Index):
		// A hook: its host kills it after a few seconds, and a rebuild on a
		// large vault can take longer, which would lose the hook's own work
		// with the rebuild half done.
		fmt.Fprintln(os.Stderr, "logos: this vault has no index yet (deleted, or never built), so what follows "+
			"answers from an empty one — a hook does not rebuild it; the next logos command does")
	case !synced:
		fmt.Fprintln(os.Stderr, "logos: this vault has no index yet (deleted, or never built), so what follows "+
			"answers from an empty one — `logos index` builds it from the vault (or `logos auto config index on`)")
	}
}

// checkIndexInBackground is checkIndex for the MCP server. Its host waits a
// few seconds for the handshake and drops a server that misses it, and a
// rebuild on a large vault takes longer — so the switch's rebuild runs as
// `logos index --auto` in a process of its own, and the server answers from
// the empty index until it lands, saying so.
func checkIndexInBackground(ix *index.Index) {
	synced, err := ix.Synced()
	if err != nil || synced || !auto.On(ix.Vault, auto.Index) {
		checkIndex(ix)
		return
	}
	if err := auto.Launch(ix.Vault, "index", "--auto"); err != nil {
		fmt.Fprintf(os.Stderr, "logos: this vault has no index (deleted, or never built), and the rebuild could not be started, "+
			"so this server answers from an empty one — `logos index` builds it: %v\n", err)
		logAutoIn(ix.Vault, auto.Entry{Switch: auto.Index, Failed: err.Error()})
		return
	}
	fmt.Fprintln(os.Stderr, "logos: this vault has no index (deleted, or never built) — auto is rebuilding it in the background, "+
		"and this server answers from an empty one until it is done")
}

// rebuildIndex is the index switch: the cache rebuilt from the vault before the
// command that found it missing answers. Text only, since a model may not be
// up and the command is waiting; `logos index` adds the vectors. Its report goes
// to stderr, because stdout is that command's answer and may be JSON.
func rebuildIndex(ix *index.Index) {
	fmt.Fprintln(os.Stderr, "logos: this vault had no index (deleted, or never built) — auto is rebuilding it from the vault, text only; `logos index` adds embeddings")
	if err := indexPass(ix, os.Stderr, nil, ""); err != nil {
		// Said as what it leaves: the command still runs, on whatever part of
		// the index the pass got through.
		fmt.Fprintf(os.Stderr, "logos: the rebuild failed, so what follows may answer from a partial index — `logos index` retries: %v\n", err)
		logAutoIn(ix.Vault, auto.Entry{Switch: auto.Index, Failed: err.Error()})
		return
	}
	logAutoIn(ix.Vault, auto.Entry{Switch: auto.Index, N: 1})
}

// openIndexQuiet is openIndex for the command that builds the index, which
// would otherwise open by telling the user to run itself.
func openIndexQuiet() (*index.Index, error) {
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
