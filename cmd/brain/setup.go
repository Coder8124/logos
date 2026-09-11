package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coder8124/brain/internal/health"
	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/provider"
	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/selfupdate"
	"github.com/Coder8124/brain/internal/setup"
	"github.com/Coder8124/brain/internal/vault"
)

// brain setup — the one command between cloning this and an agent answering
// from your vault.
//
// Every step reports and then continues. A missing model runtime does not stop
// the hosts being wired, because retrieval without a model still works and a
// half-configured machine is worse than a configured one with a warning on it.

func setupCmd(args []string) error {
	// --print-config and --config are the escape hatch for every MCP client
	// that is not one of setup.Hosts()'s curated four. Both short-circuit
	// before the vault is created or a host wired: neither one registers
	// anything brain itself can see, so neither belongs inside the interactive
	// flow that assumes it does.
	if hasFlag(args, "--print-config") {
		return printConfigCmd(args)
	}
	if path := flagStr(args, "--config", ""); path != "" {
		return configFileCmd(args, path)
	}

	opts := wireOptsFrom(args)
	yes := opts.yes

	// --dry-run has to mean it. It used to describe the plan and then carry out
	// three parts of it anyway: it created the vault directory, it *recorded*
	// that directory as this machine's vault, and it built an index inside it.
	//
	// The recording is the one that hurts. It is what the desktop app reads to
	// find the vault, and it has no environment to fall back on — so someone
	// who ran `brain setup --dry-run --vault /tmp/try-it` to see what would
	// happen had just repointed their app at an empty directory, and got a
	// healthy zero of everything with their real memory sitting untouched
	// somewhere else. That is the exact failure internal/vault/path.go was
	// written to end, reintroduced by the flag people use precisely because
	// they are being careful.
	dir, created, rec, err := chooseVault(args, opts.dryRun)
	if err != nil {
		return err
	}
	fmt.Printf("  vault      %s", dir)
	if created {
		if opts.dryRun {
			fmt.Print("   (would be created)")
		} else {
			fmt.Print("   (created)")
		}
	}
	fmt.Println()
	switch {
	case opts.dryRun && rec == recordedHere:
		fmt.Println("             would be recorded — the desktop app opens this vault too")
	case rec == recordedHere && vault.Recorded() == dir:
		fmt.Println("             recorded — the desktop app opens this vault too")
	case rec == recordFailed:
		// The error itself is already on screen. What must not follow it is the
		// environment's explanation, which would be a second, false reason.
		fmt.Println("             not recorded — this vault is in use for this run only")
		fmt.Println("             → fix the error above and run setup again to record it")
	default:
		// Say it, because the whole failure this prevents is a pointer that
		// changed without anybody seeing it happen.
		fmt.Println("             not recorded — BRAIN_VAULT names a vault for this process only")
		fmt.Println("             → pass --vault to make it this machine's vault instead")
	}

	// Everything below wants the vault to be the one we just chose, whatever
	// the environment said when the process started.
	os.Setenv("BRAIN_VAULT", dir)

	checkRuntime(yes, opts.dryRun)
	if opts.dryRun {
		fmt.Println("  index      would be built from the markdown in this vault")
	} else {
		indexVault(dir)
	}
	return wireHosts(dir, opts)
}

// wireOptsFrom reads the wiring flags shared by `setup` and `mcp install`.
func wireOptsFrom(args []string) wireOpts {
	return wireOpts{
		only:   flagStrs(args, "--host"),
		none:   hasFlag(args, "--no-hosts"),
		dryRun: hasFlag(args, "--dry-run"),
		yes:    hasFlag(args, "--yes") || hasFlag(args, "-y"),
	}
}

// chooseVault resolves where the vault lives and, unless this is a dry run,
// makes sure it exists and is the one this machine remembers.
//
// created reports whether the directory was missing, so a dry run can say what
// it would have made without making it. recorded reports whether this vault was
// written down as the machine's, which is not the same question.
//
// A vault named only by BRAIN_VAULT is deliberately not recorded. BRAIN_VAULT
// is a per-process override — it is how the documented scratch-vault workflow
// works, and how an MCP host config pins one server to one vault — so treating
// it as a machine-wide choice means a single `setup` run against a throwaway
// directory silently repoints every front end at it. That shipped: a scratch
// vault under an agent's job directory became the recorded pointer, and because
// the directory still existed, Recorded() kept returning it. Every command, the
// MCP server and the SessionStart hook then read an empty vault and truthfully
// reported nothing, while twenty-eight checkpoints sat in ~/brain. --vault, and
// the default, are choices someone made; an inherited environment variable is
// not.
// recorded says whether this vault became the machine's recorded pointer, and
// why not when it did not. The reason is load-bearing: the three ways to end up
// unrecorded — the environment chose the vault, the write failed, or this was a
// dry run — need three different next steps, and reporting one of them for all
// three told a user whose config directory was unwritable to "pass --vault",
// which is exactly what they had just done.
type recordOutcome int

const (
	recordedHere  recordOutcome = iota // written down
	recordSkipEnv                      // BRAIN_VAULT chose it, so it is this process only
	recordFailed                       // the write was attempted and failed; the error is already printed
)

func chooseVault(args []string, dryRun bool) (dir string, created bool, rec recordOutcome, err error) {
	dir = flagStr(args, "--vault", "")
	fromEnv := false
	if dir == "" {
		if v := os.Getenv("BRAIN_VAULT"); v != "" {
			dir, fromEnv = v, true
		} else {
			dir = vaultPath() // the recorded path, then ~/brain
		}
	}
	abs, err := filepath.Abs(expandHome(dir))
	if err != nil {
		return "", false, recordFailed, err
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		created = true
		if !dryRun {
			// Private from the first mkdir. A vault created world-readable and
			// tightened later is a vault that was world-readable for however long
			// the user took to run `brain doctor`.
			if err := vault.MkdirPrivate(abs); err != nil {
				return "", false, recordFailed, fmt.Errorf("creating %s: %w", abs, err)
			}
		}
	}
	// A dry run reports the outcome the real run would reach, which under
	// BRAIN_VAULT is "not recorded" — the one command whose whole job is
	// previewing was promising the opposite of what followed.
	if dryRun {
		if fromEnv {
			return abs, created, recordSkipEnv, nil
		}
		return abs, created, recordedHere, nil
	}
	if fromEnv {
		return abs, created, recordSkipEnv, nil
	}
	// Write the choice down where a front end with no shell can read it. The
	// desktop app is launched from Finder and inherits no BRAIN_VAULT, so
	// without this it can only ever find a vault at the default location.
	if err := vault.Record(abs); err != nil {
		fmt.Printf("             could not record this vault for the desktop app: %v\n", err)
		return abs, created, recordFailed, nil
	}
	return abs, created, recordedHere, nil
}

// checkRuntime reports the local model runtime and offers to pull what is
// missing. A machine with no runtime is told what to install and left working:
// lexical retrieval and the whole continuity surface need no model at all.
// dryRun turns every offer into a description. `--dry-run --yes` used to be a
// combination that downloaded models — several gigabytes, from a command whose
// last line says nothing was written.
func checkRuntime(yes, dryRun bool) {
	found := provider.Discover()
	if len(found) == 0 {
		fmt.Println("  runtime    none found")
		fmt.Println("             install Ollama (ollama.com) for semantic search;")
		fmt.Println("             without it retrieval is lexical, which still works")
		return
	}
	p := found[0].Provider
	fmt.Printf("  runtime    %s at %s\n", p.Name, p.BaseURL)

	have := map[string]bool{}
	for _, m := range found[0].Models {
		have[m] = true
		if base, _, ok := strings.Cut(m, ":"); ok {
			have[base] = true
		}
	}

	// The embedding model and the chat tiers are asked about separately, because
	// they are not the same decision and lumping them made the answer harder
	// than it needed to be.
	//
	// T0 is 274MB and buys semantic search. T1 and T2 together are ~26GB and buy
	// `ask`, `voice`, `presence` and the nightly rollup — none of which any MCP
	// tool touches, so a coding agent needs none of it. Offering all three in one
	// prompt asked people to download 26GB to get 274MB of product, with no way
	// to say "just the useful one" and no sizes to judge by.
	embed := env("BRAIN_EMBED", defaultEmbedModel)
	fmt.Printf("  embedding  %s %s\n", embed, tick(have[embed]))
	if !have[embed] {
		if dryRun {
			fmt.Printf("             would offer to pull %s (%s)\n", embed, modelSize(embed))
		} else if yes || confirm(fmt.Sprintf("             pull %s (%s)? adds semantic search",
			embed, modelSize(embed))) {
			pull(p.BaseURL, embed)
		} else {
			fmt.Println("             skipped; retrieval stays lexical, which still works")
		}
	}

	var chat []string
	for _, want := range chatModels() {
		if !have[want] {
			chat = append(chat, want)
		}
		fmt.Printf("  model      %s %s\n", want, tick(have[want]))
	}
	if len(chat) == 0 {
		return
	}

	// Default no, and say what declining costs. With the server no longer
	// refusing to start without a runtime, "no" is a safe answer rather than a
	// gamble — which is what makes stating the size honest rather than a scare.
	fmt.Printf("             %s are optional (%s) — only `brain ask`, `voice`\n",
		strings.Join(chat, " and "), totalSize(chat))
	fmt.Println("             and the nightly rollup use them. No MCP tool does.")
	if !allModels(os.Args) {
		fmt.Println("             skipped; pass --all-models to pull them")
		return
	}
	if dryRun {
		fmt.Printf("             would pull %s (%s)\n", strings.Join(chat, " and "), totalSize(chat))
		return
	}
	for _, m := range chat {
		pull(p.BaseURL, m)
	}
}

// pull fetches one model, reporting either way.
func pull(baseURL, model string) {
	fmt.Printf("             pulling %s … ", model)
	if err := pullModel(baseURL, model); err != nil {
		fmt.Printf("failed: %v\n", err)
		return
	}
	fmt.Println("done")
}

func allModels(args []string) bool { return hasFlag(args, "--all-models") }

// modelSize is what a download actually costs, so "yes" is an informed answer.
// Approximate and clearly so — the exact figure depends on the quantisation the
// registry serves, and a rounded number a user can plan around beats a precise
// one that is wrong on their machine.
func modelSize(model string) string {
	switch {
	case strings.Contains(model, "embed"):
		return "~270 MB"
	case strings.HasPrefix(model, "gemma3:4b"):
		return "~3.3 GB"
	case strings.HasPrefix(model, "qwen3"):
		return "~23 GB"
	default:
		return "size unknown"
	}
}

func totalSize(models []string) string {
	var known []string
	for _, m := range models {
		if s := modelSize(m); s != "size unknown" {
			known = append(known, s)
		}
	}
	if len(known) == 0 {
		return "size unknown"
	}
	return strings.Join(known, " + ")
}

// chatModels is the configured local chat tiers. Read from the router config
// rather than hard-coded, so setup offers what this install would actually use.
//
// Deliberately excludes the embedding model, which is a separate and much
// smaller decision — see checkRuntime.
func chatModels() []string {
	cfg, err := router.Load(vaultPath())
	if err != nil {
		return nil
	}
	var out []string
	for _, t := range []router.Tier{router.T1, router.T2} {
		if tc, ok := cfg.Tiers[t.String()]; ok && tc.Model != "" && tc.BaseURL == "" {
			out = append(out, tc.Model)
		}
	}
	return out
}

func tick(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗  missing"
}

// pullModel asks Ollama to fetch a model. The response streams progress as
// JSON lines; we only need to know it finished without an error.
func pullModel(baseURL, model string) error {
	// Ollama's native API sits alongside the OpenAI-compatible /v1 path.
	root := strings.TrimSuffix(strings.TrimSuffix(baseURL, "/"), "/v1")
	body, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return err
	}
	resp, err := http.Post(root+"/api/pull", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var line struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Error != "" {
			return fmt.Errorf("%s", line.Error)
		}
	}
	return sc.Err()
}

// indexVault runs the first index so the vault is queryable immediately.
func indexVault(vault string) {
	ix, err := index.Open(vault)
	if err != nil {
		fmt.Printf("  index      failed: %v\n", err)
		return
	}
	defer ix.Close()

	rep, err := ix.Sync()
	if err != nil {
		fmt.Printf("  index      failed: %v\n", err)
		return
	}
	// provider.Discover rather than findProvider: the latter prints a banner of
	// its own, which would interrupt this report mid-table.
	embedModel := env("BRAIN_EMBED", defaultEmbedModel)
	if found := provider.Discover(); len(found) > 0 {
		ix.EmbedPending(found[0].Provider, embedModel, 32)
		ix.SyncMemories(found[0].Provider, embedModel)
	}
	notes, _ := ix.NoteCount()
	edges, _ := ix.EdgeCount()
	fmt.Printf("  index      %d notes, %d edges", notes, edges)
	if rep.Skipped > 0 {
		fmt.Printf(" (%d skipped)", rep.Skipped)
	}
	fmt.Println()
}

// wireHosts registers this binary with every MCP host on the machine.
// wireOpts is how the caller narrows or previews the wiring.
type wireOpts struct {
	only   []string // --host, repeatable; empty means every detected host
	none   bool     // --no-hosts: set up the vault and wire nothing
	dryRun bool     // --dry-run: show the plan and change nothing
	yes    bool     // --yes: do not prompt
}

// detectHosts and integrationChecks are seams, and they exist for one reason:
// a test that reached the real ones ran `claude mcp add --scope user` and
// `codex mcp add` against the developer's own machine. Only a fake HOME kept
// the damage inside a temp directory. Nothing in a test may invoke a host's CLI
// or spawn a real MCP server.
var (
	detectHosts       = setup.Hosts
	integrationChecks = health.Integration
)

// brainServer is the command line and environment any host — known to
// setup.Hosts() or not — needs to reach this brain and this vault. Shared by
// wireHosts, --print-config and --config so that all three describe the exact
// same server; a hand-typed config that differs from what `brain setup` itself
// would have written is a bug users would have no way to notice.
func brainServer(vault string) (setup.Server, error) {
	bin, err := selfPath()
	if err != nil {
		return setup.Server{}, err
	}
	return serverFor(bin, vault), nil
}

func selfPath() (string, error) {
	bin, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not find my own path, which the host config needs: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	return bin, nil
}

// probeTarget is what the integration check launches, which is deliberately not
// always what the hosts launch.
//
// Under npx the wired command is `npx -y @noeton/logos mcp serve`, and running
// that here would make `brain doctor` fetch the package whenever npm's cache has
// been pruned — an egress from a command that promises nothing leaves the
// machine, and slow enough that the probe's ten-second handshake deadline
// expires first, reporting a perfectly healthy install as broken. The server
// binary is identical either way; npx only adds the fetch. So probe this binary
// and say out loud that the wired command differs, rather than quietly claiming
// to have tried it.
func probeTarget(self string, srv setup.Server) (bin string, args []string, note string) {
	if srv.Bin != self {
		return self, []string{"mcp", "serve"},
			fmt.Sprintf("probed this binary; hosts launch `%s %s`, which resolves the same server on demand",
				srv.Bin, strings.Join(srv.Args, " "))
	}
	return srv.Bin, srv.Args, ""
}

// serverFor is the decision brainServer makes, separated from finding this
// process's own path so it can be tested for a binary this test run is not
// executing from.
//
// The README's own install line is `npx -y @noeton/logos setup`, and under npx
// the binary lives in a cache directory npm prunes. Writing that path into a
// host config produces the worst shape of failure this product has: setup says
// "Working", and weeks later the host fails to launch a binary that is simply
// gone, with nothing tying it back to the install. npx resolves a copy on
// demand, so name the command instead of the file — which is also the config
// npm/README.md tells people to write by hand, "portable between machines,
// which an absolute binary path is not".
func serverFor(bin, vault string) setup.Server {
	// Absolute, and always written: a host launches the server from a directory
	// nobody chose, and a relative vault would silently resolve somewhere the
	// user will never look.
	env := map[string]string{"BRAIN_VAULT": vault}
	if selfupdate.DetectInstall(bin) == selfupdate.NPX {
		return setup.Server{Bin: "npx", Args: []string{"-y", "@noeton/logos", "mcp", "serve"}, Env: env}
	}
	return setup.Server{Bin: bin, Args: []string{"mcp", "serve"}, Env: env}
}

// resolvedVault is the vault --print-config and --config act on: an explicit
// --vault, falling back to the one this machine already has configured. Never
// created here — printing or merging a config is not the step that brings a
// vault into existence, and doing so behind a flag whose whole point is "just
// show me / just write this" would be the same silent-vault-creation mistake
// chooseVault's own doc comment already explains.
func resolvedVault(args []string) (string, error) {
	v := flagStr(args, "--vault", "")
	if v == "" {
		v = vaultPath()
	}
	return filepath.Abs(expandHome(v))
}

// printConfigCmd is `brain setup --print-config`: the server block by hand,
// for an MCP client that is not one of the four Hosts() knows how to find or
// register. Those clients are real — MCP has more of them than this package
// will ever special-case — and until this existed, the only answer for their
// users was silence.
func printConfigCmd(args []string) error {
	vault, err := resolvedVault(args)
	if err != nil {
		return err
	}
	srv, err := brainServer(vault)
	if err != nil {
		return err
	}
	out, err := setup.RenderConfig(srv, flagStr(args, "--format", ""))
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

// configFileCmd is `brain setup --config <path>`: merge brain into a config
// file at a location brain has no built-in convention for, reusing the exact
// merge (parse-before-touch, backup-before-write, no-op-writes-nothing)
// mergeJSON already gives Claude Desktop and Cursor.
func configFileCmd(args []string, path string) error {
	vault, err := resolvedVault(args)
	if err != nil {
		return err
	}
	srv, err := brainServer(vault)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(expandHome(path))
	if err != nil {
		return err
	}
	outcome, err := setup.MergeFile(abs, srv)
	if err != nil {
		return err
	}
	fmt.Printf("  %-16s %s (%s)\n", "config", outcome, abs)
	return nil
}

func wireHosts(vault string, opts wireOpts) error {
	// --no-hosts is for someone evaluating brain, or setting up a second vault
	// on a machine that already has one wired. Until it existed the only way to
	// create and index a vault was to also repoint every AI tool on the machine
	// at it, which is a large thing to have to accept in order to look.
	if opts.none {
		fmt.Println("\n  hosts      --no-hosts: nothing was wired")
		fmt.Println("             `brain mcp install` connects them when you are ready")
		return nil
	}
	srv, err := brainServer(vault)
	if err != nil {
		return err
	}
	bin := srv.Bin

	known := detectHosts()
	hosts, unmatched := setup.Only(known, opts.only)
	if len(unmatched) > 0 {
		// Named off the same list the match was made against. Reaching past the
		// seam to setup.Hosts() meant the message could list a roster the match
		// never consulted.
		return fmt.Errorf("unknown host %s — brain knows: %s",
			strings.Join(unmatched, ", "), strings.Join(setup.Names(known), ", "))
	}

	// Show the plan before touching anything. Registering brain with every AI
	// tool on the machine is a big commitment for someone trying one of them,
	// and it used to happen with no gate at all.
	plan := setup.Plan(hosts)
	var present int
	for _, r := range plan {
		if r.Outcome == setup.Pending {
			present++
		}
	}

	fmt.Println("\n  hosts")
	if present > 0 {
		fmt.Printf("    each of these will be pointed at:\n")
		fmt.Printf("      %s %s\n", bin, strings.Join(srv.Args, " "))
		fmt.Printf("      BRAIN_VAULT=%s\n\n", vault)
	}
	// The roster is what a person says yes or no to, so it is printed when
	// there is a decision to make — a prompt coming, or a --dry-run that is
	// nothing but the roster. Under --yes there is no decision, and printing it
	// meant the same four hosts appeared twice in a row, the second time with
	// outcomes: the most important screen in the product read as a rendering
	// fault at exactly the moment a new user is deciding whether to trust it.
	showPlan := opts.dryRun || !opts.yes
	if showPlan {
		for _, r := range plan {
			if r.Outcome == setup.Skipped {
				fmt.Printf("    %-16s —  not installed\n", r.Host)
				continue
			}
			fmt.Printf("    %-16s →  %s\n", r.Host, r.Where)
		}
	}

	if present == 0 {
		fmt.Println("\n  No MCP hosts found. Install Claude Code, Claude Desktop, Cursor or")
		fmt.Println("  Codex and re-run `brain mcp install`.")
		if opts.dryRun {
			// Said even here. A dry run that ends without its disclaimer, because
			// it happened to find nothing to wire, is a dry run the reader has to
			// guess about — and the vault half of it did have something to say.
			fmt.Println("\n  --dry-run: nothing was written.")
		}
		return nil
	}

	if opts.dryRun {
		fmt.Println("\n  --dry-run: nothing was written.")
		return nil
	}
	if !opts.yes && !confirm(fmt.Sprintf("\n  wire %d host(s)?", present)) {
		fmt.Println("  skipped; re-run `brain mcp install` when you are ready")
		fmt.Println("  (--host <name> wires just one)")
		return nil
	}

	if showPlan {
		fmt.Println() // separate the roster above from the outcomes below
	}
	var wired int
	for _, r := range setup.Install(srv, hosts) {
		switch r.Outcome {
		case setup.Skipped:
			fmt.Printf("    %-16s —  not installed\n", r.Host)
		case setup.Failed:
			fmt.Printf("    %-16s ✗  %v\n", r.Host, r.Err)
		default:
			wired++
			fmt.Printf("    %-16s ✓  %s (%s)\n", r.Host, r.Outcome, r.Where)
			// A registration replaces what was there — `codex mcp add` drops
			// the whole previous entry, environment and all. Naming the copy is
			// what makes a wrong --vault recoverable.
			if r.Backup != "" {
				fmt.Printf("    %-16s    previous config saved as %s\n", "", r.Backup)
			}
		}
	}

	// Writing a config file is not the same as having a working integration,
	// and until now nothing checked the difference — the first evidence of a
	// problem arrived inside the host, as a connection error with no diagnosis.
	// Being our own host for one round trip costs a second and turns "wrote the
	// config" into "this works".
	if wired > 0 {
		fmt.Println("\n  checking it works")
		ok := true
		probeBin, probeArgs, note := probeTarget(bin, srv)
		if note != "" {
			fmt.Printf("    %-16s    %s\n", "", note)
		}
		for _, c := range integrationChecks(probeBin, probeArgs, vault) {
			if c.State == health.Failed {
				ok = false
				fmt.Printf("    %-16s ✗  %s\n", c.Name, c.Detail)
				if c.Fix != "" {
					fmt.Printf("    %-16s    → %s\n", "", c.Fix)
				}
				continue
			}
			fmt.Printf("    %-16s ✓  %s\n", c.Name, c.Detail)
		}
		if !ok {
			fmt.Println("\n  The hosts are configured but the server did not pass its own check.")
			fmt.Println("  `brain doctor --integration` re-runs this.")
			return nil
		}
	}

	if wired == 0 {
		fmt.Println("\n  No MCP hosts found. Install Claude Code, Claude Desktop, Cursor or")
		fmt.Println("  Codex and re-run `brain mcp install`.")
		return nil
	}
	// The first thing a new user sees brain do decides what they think it is.
	// "What do you remember about me?" on a fresh vault correctly answers
	// "nothing", which demonstrates an empty database rather than the product.
	//
	// Continuity is the differentiator and it works immediately, with no model
	// and no indexing: a checkpoint is markdown, and resume reads it back. So
	// the suggested first move is a handoff someone can run in thirty seconds
	// and watch survive across two different agents.
	fmt.Println("\n  Restart the host, then try the handoff — it is what brain is for:")
	fmt.Println()
	fmt.Println("    1. In one agent:  \"checkpoint this: trying X, ruled out Y because Z,")
	fmt.Println("                       next step is W\"")
	fmt.Println("    2. In another:    \"resume <project>\"")
	fmt.Println()
	fmt.Println("  The second agent should recite what the first ruled out, without")
	fmt.Println("  you re-explaining. From the terminal: brain resume <project>")
	return nil
}

// mcpInstallCmd is the wiring on its own, for someone who already has a vault.
func mcpInstallCmd(args []string) error {
	vault := flagStr(args, "--vault", "")
	if vault == "" {
		vault = vaultPath()
	}
	abs, err := filepath.Abs(expandHome(vault))
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("vault not found at %s — run `brain setup` first, or pass --vault", abs)
	}
	return wireHosts(abs, wireOptsFrom(args))
}

// confirm asks a yes/no question. An interactive user pressing return accepts;
// nobody being there declines.
//
// It used to accept on EOF, which meant a setup run from a script, a CI job or
// a piped installer silently answered yes to everything — including a
// multi-gigabyte model pull nobody asked for. A prompt with no reader is not
// consent. `--yes` remains the way to say yes without a terminal, and it is
// explicit.
func confirm(prompt string) bool {
	fmt.Printf("%s [Y/n] ", prompt)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		fmt.Println("\n             no answer (not a terminal) — skipping; pass --yes to accept")
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(sc.Text()))
	return answer == "" || answer == "y" || answer == "yes"
}

// expandHome resolves a leading ~ so --vault ~/brain works from any shell.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}
