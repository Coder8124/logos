// Command brain is the CLI front end. The same packages back the Wails app, so
// there is one engine rather than two.
package main

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Coder8124/brain/internal/buildinfo"
	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/capture/sources"
	"github.com/Coder8124/brain/internal/health"
	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/provider"
	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/session"
	"github.com/Coder8124/brain/internal/vault"
	"github.com/Coder8124/brain/internal/voice"
)

// version is what `brain version` prints. It comes from internal/buildinfo so
// that this and the version the MCP handshake announces are the same string —
// they used to be two, and one of them was a literal nobody remembered to bump.
//
// "dev" is what a plain `go build` produces, and saying so is more useful than
// printing a number that is not tied to a release.
var version = buildinfo.Version

const (
	defaultEmbedModel = "nomic-embed-text"
	defaultChatModel  = "qwen3.6"
	// Default first-run reach. Deliberately not "everything" — see PollOnce.
	defaultBackfillDays = 7
)

// The help is two surfaces, and the split is a product decision rather than a
// tidying one. brain grew about forty verbs, and printing all of them was an
// honest inventory that answered the wrong question: a first-time reader wants
// to know what this is *for*, and forty lines of episodic capture, voice and
// benchmarks say "a grab-bag" no matter what the first line claims.
//
// So the default is the three journeys the product is actually about — hand
// off, brief, intercept — plus the three commands that get you there. Nothing
// is hidden: `brain help all` is the old inventory, grouped, and every verb
// still works exactly as it did. This changes what help prints, not what brain
// does.

// helpShort is what `brain`, `brain help` and `brain --help` all print. It is
// deliberately one screen: the handoff is the centre of the product, and it is
// what a reader should be able to try in the next thirty seconds.
func helpShort(w io.Writer) {
	fmt.Fprint(w, `brain — local-first memory and continuity for AI agents

Agents forget the moment a session ends. brain is the memory they hand to one
another: one stops, the next picks up exactly where it left off.

THE HANDOFF — an agent finishes, and another continues
    brain note <project> <what you did>
                                      record progress; uncommitted until you checkpoint
    brain checkpoint <project> [--task ..] [--next ..] [--failed ..] [--handoff <agent>]
                                      commit where you stopped, as a note in the vault
    brain resume <project>            pick up where the last agent left off

THE BRIEF — what bears on the work, before the work starts
    brain context <task> [--project <p>] [--budget <n>]
                                      everything bearing on a task, budgeted (also an MCP tool)

THE INTERCEPT — the dead end nobody remembers recording
    brain tried <approach> [--project X]
                                      has this already been ruled out? ask before proposing

GETTING THERE
    brain setup [--vault DIR] [--host NAME] [--dry-run] [--yes] [--all-models]
                                      connect brain to the AI agents on this machine
    brain mcp serve | mcp install     serve the memory to MCP hosts; wire the ones found
    brain doctor [--probe] [--integration]
                                      health of vault, index, hosts; --integration proves reach

    BRAIN_VAULT points at the vault (default ~/brain)

`+"`brain help all`"+` lists the rest — memory, capture, rollups, voice, benchmarks.
`)
}

// helpAll is the full inventory, grouped. It exists so that demoting the
// general surface does not amount to hiding it: everything brain has ever
// accepted is here, spelled the way you type it.
func helpAll(w io.Writer) {
	fmt.Fprintf(w, `brain — local-first memory and continuity for AI agents

CONTINUITY
    brain note <project> <what you did>
                                      record progress; uncommitted until you checkpoint
    brain checkpoint <project> [--task ..] [--next ..] [--failed ..] [--handoff <agent>]
                                      commit where you stopped, as a note in the vault
    brain resume <project>            pick up where the last agent left off
    brain sessions <project>          checkpoint history for a project, and any abandoned ones
    brain continuity                  vault-wide: which projects checkpoint, which have gone quiet
    brain bootstrap [project] [--dry-run] [--months N]
                                      seed a cold vault from this repo's git history
    brain context <task> [--project <p>] [--budget <n>]
                                      everything bearing on a task, budgeted (also an MCP tool)
    brain tried <approach> [--project X]
                                      has this already been ruled out? ask before proposing
    brain why <file>                  what was being decided when this file was touched
    brain projects | project <name>   auto-detected projects and their dossiers

MEMORY
    brain memory [add <fact>|forget <id>|log|history <id>|graph|diff]   persistent memory
    brain memory log [--project P] [--n N]   what changed in what it knows, newest first
    brain activity [--project P] [--kind K] [--tool T] [--days N] [--json]
                                      every prompt, tool call and turn the host reported —
                                      recorded automatically, not by the model's choice
    brain activity --projects         which projects are being recorded
    brain announce [on|quiet|off]     how loudly Logos reports its own work
    brain prompt                      the instructions agents are given (BRAINPROMPT.md)
    brain demo [--fast]               ninety seconds showing what this is for, in a scratch vault
    brain memory diff [subject] [--since D] [--until D] [--days N]   what changed, instant & offline
    brain jot <thought>               braindump: capture and auto-file a thought
    brain loop [add|done|drop]        manage open loops (commitments)
    brain graph [focus] [--hops N] [--similar]   memory graph around a note

RETRIEVAL
    brain search <query…>             retrieve only, no generation
    brain ask <question…>             retrieve and answer from the vault
    brain index [--watch]             sync vault into the cache and embed

BRIEFINGS
    brain brief                       what the secretary thinks you should know now
    brain replay [--peek]             catch up on what changed since you were last here
    brain reflect                     descriptive stats over your memory (composition, growth, what it leans on)
    brain weekly                      Sunday executive briefing: your week in review

THE DAY
    brain capture [--daemon] [--backfill-days N]
                                      pull episodic events
    brain timeline [--verbose]        today's activity
    brain rollup [--date YYYY-MM-DD] [--dry-run]
                                      distil a day into a note and proposals
    brain review [--all]              accept or reject queued proposals and quarantined memories
    brain dream [--date YYYY-MM-DD] [--phase nrem|rem] [--dry-run]
                                      nightly consolidation: replay, gist, downscale, recombine
    brain dream review | accept|reject <id>
                                      review the connections REM proposed overnight
    brain routines [--days N] [--propose]
                                      mine recurring patterns from the timeline
    brain prune [days]                drop raw events past the retention window

THE ASSISTANT
    brain voice | listen | say <text>   talk to the assistant and hear it back (local STT/TTS)
    brain name [<name>]               name the assistant — how you address it
    brain presence [--wake]           the ambient secretary: greets, answers, and speaks up (--wake to talk by name)
    brain think [off|low|medium|high]  how much the model reasons before answering

SETUP AND DIAGNOSTICS
    brain setup [--vault DIR] [--host NAME] [--dry-run] [--yes] [--all-models]
                                      connect brain to the AI agents on this machine
    brain mcp serve                   serve the memory layer to MCP hosts (Claude Desktop, Cursor, your own apps)
    brain mcp install [--vault DIR] [--host NAME] [--dry-run] [--yes]
                                      register this brain with the MCP hosts found
    brain doctor [--probe] [--integration]
                                      health of vault, index, hosts; --integration proves a host can reach it
    brain key set|rm <ref>            manage API keys in the macOS keychain
    brain version                     which build this is
    brain help [all]                  the three core journeys, or this list

BENCHMARKS
    brain bench continuity [list] [--only X] [--verbose] [--brain-only]
                                      the handoff + memory suite, against every system installed
    brain bench memory <file> | bench pipeline
                                      LongMemEval retrieval recall; the extract→recall loop

ENV
    BRAIN_VAULT   path to the vault (default ~/brain)
    BRAIN_MODEL   chat model (default %s)
    BRAIN_EMBED   embed model (default %s)
    BRAIN_REPOS   colon-separated repos to mine for commits
`, defaultChatModel, defaultEmbedModel)
}

// usage is the failure path — no arguments, or a verb nobody recognises. It
// prints the short help to stderr and exits non-zero, because a command line
// that could not be parsed is an error even though the text is identical to
// what `brain help` prints on success.
func usage() {
	helpShort(os.Stderr)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	rest := strings.Join(args, " ")

	var err error
	switch {
	case cmd == "version", cmd == "--version", cmd == "-v":
		fmt.Printf("brain %s %s/%s %s\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	case cmd == "help", cmd == "--help", cmd == "-h":
		// Asked for, so it goes to stdout and exits 0 — the difference between
		// answering a question and reporting a bad command line. `help all`
		// takes the flag spelling too, so `brain --help all` is not a puzzle.
		if firstNonFlag(args) == "all" {
			helpAll(os.Stdout)
			break
		}
		helpShort(os.Stdout)
	case cmd == "setup":
		err = setupCmd(args)
	case cmd == "mcp" && len(args) >= 1 && args[0] == "install":
		err = mcpInstallCmd(args)
	case cmd == "doctor":
		if hasFlag(args, "--integration") {
			err = doctorIntegration()
			break
		}
		err = doctor(hasFlag(args, "--probe"))
	case cmd == "key":
		err = keyCmd(args)
	case cmd == "index":
		err = runIndex(hasFlag(args, "--watch"))
	case cmd == "search" && rest != "":
		err = search(rest)
	case cmd == "ask" && rest != "":
		err = ask(rest)
	case cmd == "context" && rest != "":
		err = runContext(args)
	case cmd == "note":
		err = runNote(args)
	case cmd == "checkpoint":
		err = runCheckpoint(args)
	case cmd == "resume":
		err = runResume(args)
	case cmd == "bootstrap":
		err = runBootstrap(args)
	case cmd == "why":
		err = runWhy(args)
	case cmd == "tried":
		err = runTried(args)
	case cmd == "sessions":
		err = runSessionLog(args)
	case cmd == "continuity":
		err = runContinuity(args)
	case cmd == "say" && rest != "":
		err = runSay(rest)
	case cmd == "listen":
		err = runListen(flagInt(args, "--seconds", 15))
	case cmd == "voice":
		err = runVoiceChat(flagInt(args, "--seconds", 15))
	case cmd == "presence":
		err = runPresence(hasFlag(args, "--wake"))
	case cmd == "name":
		err = runName(rest)
	case cmd == "think":
		err = runThink(rest)
	case cmd == "capture":
		err = runCapture(hasFlag(args, "--daemon"), flagInt(args, "--backfill-days", defaultBackfillDays))
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
	case cmd == "project" && len(args) > 0 && args[0] == "rename":
		err = runProjectRename(args[1:])
	case cmd == "timeline":
		err = timeline(hasFlag(args, "--verbose"))
	case cmd == "rollup":
		err = runRollup(flagStr(args, "--date", ""), hasFlag(args, "--dry-run"))
	case cmd == "review":
		err = runReview(hasFlag(args, "--all"))
	case cmd == "dream":
		err = dreamCmd(args)
	case cmd == "replay":
		err = runReplay(hasFlag(args, "--peek"))
	case cmd == "reflect":
		err = runReflect()
	case cmd == "brief":
		err = runBrief()
	case cmd == "loop":
		err = commitmentCmd(args)
	case cmd == "jot" && rest != "":
		err = jotCmd(rest)
	case cmd == "memory":
		err = memoryCmd(args)
	case cmd == "weekly":
		err = runWeekly(hasFlag(args, "--json"))
	case cmd == "projects":
		err = projectsCmd(args)
	case cmd == "project":
		err = projectCmd(args)
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
		err = runGraph(firstNonFlag(args), flagInt(args, "--hops", 2), hasFlag(args, "--similar"))
	case cmd == "routines":
		err = runRoutines(flagInt(args, "--days", 60), hasFlag(args, "--propose"))
	case cmd == "prune":
		err = prune(int64(argInt(args, 0, 90)))
	default:
		usage()
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

func flagStr(args []string, name, def string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
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
			// Skip the value too, unless the flag was given last with nothing
			// after it — in which case there is no value to skip.
			if i+1 < len(args) {
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
		if a != name || i+1 >= len(args) {
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

// vaultPath resolves where the vault lives. The rule itself lives in
// internal/vault, because the desktop app needs the same answer and having its
// own copy is how it came to open a different vault than the CLI on the same
// machine.
func vaultPath() string { return vault.Path() }

func watchedRepos() []string {
	if v := os.Getenv("BRAIN_REPOS"); v != "" {
		return strings.Split(v, ":")
	}
	if wd, err := os.Getwd(); err == nil {
		return []string{wd}
	}
	return nil
}

// findProvider picks the first running local runtime. Cloud BYOK slots in here
// later by reading a configured base URL and key.
func findProvider() (*provider.Provider, error) {
	found := provider.Discover()
	if len(found) == 0 {
		return nil, fmt.Errorf("no local model runtime found — start Ollama, LM Studio, Jan or Msty")
	}
	p := found[0]
	fmt.Fprintf(os.Stderr, "· %s at %s (%d models)\n", p.Provider.Name, p.Provider.BaseURL, len(p.Models))
	return p.Provider, nil
}

// missingVaultError is what every command says when the vault is not there.
//
// It used to say "set BRAIN_VAULT", which is the one instruction that cannot
// help either person who sees it: someone who has never run setup does not have
// a vault to point the variable at, and someone whose BRAIN_VAULT is a typo has
// already set it. Name the path that was tried, then the command that makes one.
func missingVaultError(v string) error {
	return fmt.Errorf("vault not found at %s — run `brain setup` to create one, "+
		"or point BRAIN_VAULT at an existing vault", v)
}

func openIndex() (*index.Index, error) {
	v := vaultPath()
	if _, err := os.Stat(v); err != nil {
		return nil, missingVaultError(v)
	}
	return index.Open(v)
}

// openEvents opens the index and ensures the episodic tables exist alongside it.
func openEvents() (*index.Index, error) {
	ix, err := openIndex()
	if err != nil {
		return nil, err
	}
	if err := capture.InitStore(ix.DB); err != nil {
		ix.Close()
		return nil, err
	}
	return ix, nil
}

func doctor(probe bool) error {
	// The product first, the model plumbing second. This used to be the other
	// way round — and in fact only ever reported the plumbing, so a vault that
	// did not exist and an index a week stale both passed silently.
	//
	// It also used to return an error when no runtime answered, which made the
	// one command a confused user reaches for refuse to run precisely when
	// something was wrong.
	rep := gatherHealth()
	fmt.Println("─── brain ───")
	for _, c := range rep.Checks {
		fmt.Printf("  %-14s %s\n", c.Name, renderState(c.State))
		if c.Detail != "" {
			fmt.Printf("  %-14s   %s\n", "", c.Detail)
		}
		if c.Fix != "" {
			fmt.Printf("  %-14s   → %s\n", "", c.Fix)
		}
	}
	ok, failed, unknown := rep.Counts()
	fmt.Printf("\n  %d ok · %d failed · %d unchecked\n", ok, failed, unknown)

	found := provider.Discover()
	if len(found) == 0 {
		// Not an error. Every continuity tool works without a model, and search
		// falls back to lexical; the report above already said so.
		fmt.Println("\nNo local model runtime — nothing above depends on one.")
		return nil
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

	fmt.Println("\n─── voice ───")
	for _, line := range voice.New().Status() {
		fmt.Println(" ", line)
	}

	if !probe {
		fmt.Println("\nrun `brain doctor --probe` to verify each model actually loads")
		return nil
	}

	// Listing a model proves nothing: a corrupt pull lists fine and fails on
	// load. Probing is what catches it before a rollup does at 3am.
	fmt.Println("\n─── probe ───")
	for _, t := range []router.Tier{router.T1, router.T2} {
		model, err := rt.Model(t)
		if err != nil {
			fmt.Printf("  %s  %v\n", t, err)
			continue
		}
		cap := rt.Probe(model)
		switch {
		case !cap.Loads:
			fmt.Printf("  %s  %-24s FAILS TO LOAD — %s\n", t, model, truncate(cap.Err, 70))
		case !cap.StructuredOutput:
			fmt.Printf("  %s  %-24s loads, but ignores JSON schemas\n", t, model)
		default:
			fmt.Printf("  %s  %-24s ok, honours JSON schemas\n", t, model)
		}
	}
	return nil
}

// doctorIntegration is the difference between "brain is installed" and "your
// agents can reach this vault". It is the same probe setup runs, exposed so it
// can be re-run after a host update or a config edit.
func doctorIntegration() error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	vault := vaultPath()

	fmt.Printf("─── integration ───\n  binary  %s\n  vault   %s\n\n", bin, vault)
	checks := health.Integration(bin, vault)
	failed := 0
	for _, c := range checks {
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
	if failed > 0 {
		return fmt.Errorf("integration is not working")
	}
	fmt.Println("\n  Working. A host launching this binary reaches this vault.")
	return nil
}

// gatherHealth assembles what the checks need, tolerating every piece of it
// being missing. A vault that will not open, an index that is not there and a
// runtime that is not running each become Unknown rather than an early return —
// the point of the report is to work when things are broken.
func gatherHealth() health.Report {
	vault := vaultPath()
	in := health.Input{Vault: vault, EmbedModel: env("BRAIN_EMBED", defaultEmbedModel)}

	// Stat before opening, because index.Open creates <vault>/.brain and that
	// brings the vault itself into existence. Opening it here meant doctor made
	// the vault it was about to check and then pronounced it healthy — the
	// "does not exist" branch in checkVault could not fire from the CLI at all.
	// A mistyped BRAIN_VAULT, or doctor run before setup, produced a second
	// empty vault with a clean bill of health, which is exactly the "healthy
	// zero of everything" that internal/vault/path.go exists to prevent.
	//
	// A vault that exists but has never been indexed is a different case, and
	// index.Open creating .brain for that one is wanted.
	if _, err := os.Stat(vault); err == nil {
		if ix, err := index.Open(vault); err == nil {
			defer ix.Close()
			capture.InitStore(ix.DB) // so the capture check reads a table rather than an error
			session.Init(ix.DB)      // so the abandonment check reads a table rather than an error
			in.DB = ix.DB
		}
	}
	if found := provider.Discover(); len(found) > 0 {
		in.Runtime = found[0].Provider
	}
	in.RetentionDays, in.KeepForever = captureRetention(vault)

	return health.Run(in)
}

func renderState(s health.State) string {
	switch s {
	case health.OK:
		return "ok"
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
		return fmt.Errorf("usage: brain key set|rm <ref>")
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
	return fmt.Errorf("usage: brain key set|rm <ref>")
}

func runIndex(watch bool) error {
	ix, err := openIndex()
	if err != nil {
		return err
	}
	defer ix.Close()

	// Sync is pure file reading — it needs no model, and it is what keeps the
	// FTS table current. Only the embedding passes need a provider.
	//
	// Requiring one here left a hole in the middle of the no-runtime story:
	// lexical search worked, but the command that refreshes what it searches did
	// not, so editing a note on a machine without Ollama meant the change was
	// invisible until a model appeared. Worse, `brain checkpoint` tells the user
	// to run exactly this command.
	p, perr := findProvider()
	embedModel := env("BRAIN_EMBED", defaultEmbedModel)
	if perr != nil {
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
		if restored, err := ix.SyncNotes(); err != nil {
			fmt.Fprintln(os.Stderr, "· could not restore working notes:", err)
		} else if restored > 0 {
			fmt.Printf("restored %d uncommitted working %s\n", restored, plural(restored, "note"))
		}

		// Memories and the review queue come back with or without a model.
		// Import needs a provider only to re-embed, and passing a nil one skips
		// exactly that — so this used to sit behind the `p == nil` return
		// below, which meant a rebuild on a machine with no runtime restored
		// the notes and left every remembered fact out of the cache until some
		// later run happened to have Ollama up. "Delete the index, lose
		// nothing" cannot depend on a model being reachable.
		mems, err := ix.SyncMemories(p, embedModel)
		if err != nil {
			return err
		}

		// The review queue, after the memories, so an accepted proposal is
		// already an active memory before the queue is consulted about its id.
		if queued, err := ix.SyncPending(); err != nil {
			fmt.Fprintln(os.Stderr, "· could not restore the review queue:", err)
		} else if queued > 0 {
			fmt.Printf("restored %d memor%s awaiting review — run `brain review`\n",
				queued, pluralY(queued))
		}

		if p == nil {
			fmt.Printf("+%d ~%d -%d =%d · %d notes, %d edges, %d memories · lexical only\n",
				rep.Added, rep.Updated, rep.Removed, rep.Unchanged, notes, edges, mems)
			return nil
		}

		embedded, err := ix.EmbedPending(p, embedModel, 32)
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
	if p, perr := findProvider(); perr == nil {
		hits, err = ix.HybridSearch(p, env("BRAIN_EMBED", defaultEmbedModel), query, 8)
	} else {
		fmt.Fprintln(os.Stderr, "· no model runtime — searching lexically")
		hits, err = ix.LexicalSearch(query, 8)
	}
	if err != nil {
		return err
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

	answer, hits, err := ix.Ask(p,
		env("BRAIN_EMBED", defaultEmbedModel),
		env("BRAIN_MODEL", defaultChatModel),
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

func scratchDir(vault string) string { return filepath.Join(vault, ".brain", "scratch") }

func runCapture(daemon bool, backfillDays int) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	policy := capture.DefaultPolicy()
	repos := watchedRepos()
	backfill := int64(backfillDays) * 86400

	n, err := capture.PollOnce(ix.DB, scratchDir(ix.Vault), repos, policy, backfill)
	if err != nil {
		return err
	}
	total, _ := capture.Count(ix.DB)
	fmt.Printf("+%d events · %d total\n", n, total)

	if !daemon {
		return nil
	}

	front := sources.ProbeFrontmost()
	if front.Granularity == sources.AppAndTitle {
		fmt.Println("· focus sampling: app + window title")
	} else {
		fmt.Println("· focus sampling: app name only — grant Accessibility to System Events for window titles")
	}

	// Say what this will cost before it starts costing it. A daemon that samples
	// every few seconds and keeps what it finds is a reasonable thing to run and
	// an unreasonable thing to discover you have been running.
	retentionDays, keepForever := captureRetention(ix.Vault)
	describeCapture(ix.DB, retentionDays, keepForever)
	fmt.Println("· recording. ^C to stop.")

	// Sessions are written only when they end, so a crash loses at most the one
	// in flight. That is the right trade against writing every 5s sample.
	coalescer := capture.NewCoalescer(60)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	ticker := time.NewTicker(sources.PollInterval)
	defer ticker.Stop()
	// Browser and git are pull sources; polling them every 5s would be wasteful,
	// so they run on their own slower cadence.
	pullTicker := time.NewTicker(5 * time.Minute)
	defer pullTicker.Stop()

	// The nightly dream. Checked hourly and run at most once per calendar day,
	// past dreamHour, over the day that just ended. lastDream lives in memory, so
	// a restart may re-run a day once — the pass is deterministic in NREM and
	// dedups its gists, and REM is capped, so a re-run is cheap rather than
	// harmful. (Persisting the last-dream date is a follow-up.)
	dreamTicker := time.NewTicker(time.Hour)
	defer dreamTicker.Stop()
	var lastDream string

	// The ambient presence: speaks up about a meeting, a slipping loop, or an
	// overnight insight while you work — restrained (cooldown-spaced, focus-aware)
	// and never overriding your attention. Focus is tracked from the same
	// frontmost samples capture already takes.
	dp := newDaemonPresence(ix.DB, ix.Vault)
	presenceTicker := time.NewTicker(time.Minute)
	defer presenceTicker.Stop()

	for {
		select {
		case <-stop:
			if done := coalescer.Flush(); done != nil {
				capture.Insert(ix.DB, *done)
			}
			fmt.Println("\n· stopped, session flushed")
			return nil

		case <-ticker.C:
			sample, err := front.Sample()
			if err != nil || policy.ShouldDrop(sample) {
				continue
			}
			dp.track(sample.App, time.Now())
			if done := coalescer.Push(sample); done != nil {
				if err := capture.Insert(ix.DB, *done); err != nil {
					fmt.Fprintln(os.Stderr, "· write error:", err)
				}
			}

		case <-pullTicker.C:
			n, err := capture.PollOnce(ix.DB, scratchDir(ix.Vault), repos, policy, backfill)
			if err != nil {
				fmt.Fprintln(os.Stderr, "· poll error:", err)
			} else if n > 0 {
				fmt.Printf("+%d events\n", n)
			}

		case <-dreamTicker.C:
			// Retention rides the hourly tick rather than getting a ticker of its
			// own: the window is measured in days, so the exact hour it runs does
			// not matter, and one fewer goroutine does.
			if !keepForever {
				cutoff := time.Now().AddDate(0, 0, -retentionDays).Unix()
				if n, err := capture.Prune(ix.DB, cutoff); err != nil {
					fmt.Fprintln(os.Stderr, "· prune error:", err)
				} else if n > 0 {
					fmt.Printf("· pruned %d events older than %d days\n", n, retentionDays)
				}
			}

			today := time.Now().Format("2006-01-02")
			if time.Now().Hour() >= dreamHour && lastDream != today {
				lastDream = today
				if err := dreamNightly(ix.DB, ix.Vault); err != nil {
					fmt.Fprintln(os.Stderr, "· dream error:", err)
				}
			}

		case <-presenceTicker.C:
			dp.tick(ix.DB, time.Now())
		}
	}
}

// captureRetention reads the window from config, falling back to the default
// when the config cannot be read. A malformed config should not mean "keep
// everything forever" by accident.
func captureRetention(vault string) (days int, forever bool) {
	cfg, err := router.Load(vault)
	if err != nil {
		return router.DefaultRetentionDays, false
	}
	return cfg.Retention()
}

// describeCapture prints what the daemon samples, how long it keeps it, and
// what that costs — measured from the user's own events rather than estimated
// from a table of averages.
func describeCapture(db *sql.DB, retentionDays int, keepForever bool) {
	fmt.Printf("· sampling every %s · browser, calendar and git every 5m\n",
		sources.PollInterval)

	if keepForever {
		fmt.Println("· retention: keeping everything (retention_days is negative in config)")
	} else {
		fmt.Printf("· retention: %d days, pruned hourly\n", retentionDays)
	}

	events, bytes, days, err := capture.Footprint(db)
	if err != nil {
		return
	}
	if events == 0 {
		fmt.Println("· disk: nothing recorded yet, so there is nothing to project from")
		return
	}
	fmt.Printf("· disk: %s across %d events", humanBytes(bytes), events)
	// Under a day of history cannot support a weekly projection. Saying so is
	// better than multiplying ten minutes by 1008 and presenting the result.
	if days < 1 {
		fmt.Println(" — too little history to project a weekly rate yet")
		return
	}
	perWeek := float64(bytes) / days * 7
	fmt.Printf(" · about %s/week at this rate", humanBytes(int64(perWeek)))
	if !keepForever {
		fmt.Printf(", levelling off near %s", humanBytes(int64(perWeek/7*float64(retentionDays))))
	}
	fmt.Println()
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func timeline(verbose bool) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	from, to := capture.TodayBounds()
	events, err := capture.Range(ix.DB, from, to)
	if err != nil {
		return err
	}

	fmt.Print(capture.Render(events, verbose))

	if totals := capture.ByApp(events); len(totals) > 0 {
		fmt.Println("\n─── time by app ───")
		for i, t := range totals {
			if i >= 10 {
				break
			}
			fmt.Printf("  %-20s %s\n", t.App, capture.Dur(t.Secs))
		}
	}
	return nil
}

func prune(days int64) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	n, err := capture.Prune(ix.DB, capture.Now()-days*86400)
	if err != nil {
		return err
	}
	remain, _ := capture.Count(ix.DB)
	fmt.Printf("dropped %d events older than %dd · %d remain\n", n, days, remain)
	return nil
}
