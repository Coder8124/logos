package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
	"github.com/Coder8124/logos/internal/selfupdate"
	"github.com/Coder8124/logos/internal/setup"
	"github.com/Coder8124/logos/internal/vault"
)

// logos setup — the one command between cloning this and an agent answering
// from your vault.
//
// Every step reports and then continues. A missing model runtime does not stop
// the hosts being wired, because retrieval without a model still works and a
// half-configured machine is worse than a configured one with a warning on it.

func setupCmd(args []string) error {
	// Checked before anything else: setup creates and records a vault and, at
	// a terminal, offers to wire every host with Enter meaning yes. Asking for
	// the flags must not be one keystroke from an install.
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Print(setupUsage)
		return nil
	}
	args, err := normalizeSetupFlags(args)
	if err != nil {
		return err
	}
	// --print-config and --config are the escape hatch for every MCP client
	// that is not one of setup.Hosts()'s curated four. Both short-circuit
	// before the vault is created or a host wired: neither one registers
	// anything logos itself can see, so neither belongs inside the interactive
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
	// who ran `logos setup --dry-run --vault /tmp/try-it` to see what would
	// happen had just repointed their app at an empty directory, and got a
	// healthy zero of everything with their real memory sitting untouched
	// somewhere else. That is the exact failure internal/vault/path.go was
	// written to end, reintroduced by the flag people use precisely because
	// they are being careful.
	previous := vault.Recorded()
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
	case rec == recordSkipTemp:
		fmt.Println("             not recorded — a temporary directory is used for this run only")
		fmt.Println("             → pass --yes to record it anyway, or --vault somewhere that lasts")
	case rec == recordFailed:
		// The error itself is already on screen. What must not follow it is the
		// environment's explanation, which would be a second, false reason.
		fmt.Println("             not recorded — this vault is in use for this run only")
		fmt.Println("             → fix the error above and run setup again to record it")
	default:
		// Say it, because the whole failure this prevents is a pointer that
		// changed without anybody seeing it happen.
		fmt.Println("             not recorded — LOGOS_VAULT names a vault for this process only")
		fmt.Println("             → pass --vault to make it this machine's vault instead")
	}

	// Everything below wants the vault to be the one we just chose, whatever
	// the environment said when the process started.
	os.Setenv("LOGOS_VAULT", dir)

	checkRuntime(yes, opts.dryRun)
	if opts.dryRun {
		fmt.Println("  index      would be built from the markdown in this vault")
	} else {
		indexVault(dir)
	}
	// "This run only" has to be true of the hosts too: a host config outlives
	// the run, so wiring them to a vault nobody recorded made it permanent.
	// A dry run still previews the wiring: the real run asks, and may record it.
	// A temporary vault recorded by an earlier run is already permanent, and
	// refusing its hosts would only leave them on whatever they had before.
	if rec == recordSkipTemp && vault.Recorded() != dir && !opts.dryRun && !opts.none {
		return fmt.Errorf("no hosts were wired: %s was not recorded, and a host config would keep it after this run — pass --yes to record and wire it, or --vault somewhere that lasts", dir)
	}
	if err := wireHosts(dir, opts); err != nil {
		return err
	}
	// The record is machine-wide but --host, or a no at the prompt, wires only
	// some hosts. Said after wiring, so only the hosts actually left behind are
	// named.
	if !opts.dryRun && rec == recordedHere && previous != "" && filepath.Clean(previous) != dir {
		if names, vaults := setup.OnOtherVault(detectHosts(), dir); len(names) > 0 {
			fmt.Printf("\n  vault      moved from %s — still on a different vault:\n", previous)
			for i, n := range names {
				fmt.Printf("    %-*s →  %s\n", hostColumn, n, vaults[i])
			}
			fmt.Printf("             → run `logos mcp install` to move them here too\n")
		}
	}
	return nil
}

// wireOptsFrom reads the wiring flags shared by `setup` and `mcp install`.
func wireOptsFrom(args []string) wireOpts {
	return wireOpts{
		only:   flagStrs(args, "--host"),
		none:   hasFlag(args, "--no-hosts"),
		dryRun: hasFlag(args, "--dry-run"),
		yes:    hasFlag(args, "--yes") || hasFlag(args, "-y"),

		downgrade: hasFlag(args, "--downgrade"),
	}
}

// chooseVault resolves where the vault lives and, unless this is a dry run,
// makes sure it exists and is the one this machine remembers.
//
// created reports whether the directory was missing, so a dry run can say what
// it would have made without making it. recorded reports whether this vault was
// written down as the machine's, which is not the same question.
//
// A vault named only by LOGOS_VAULT is deliberately not recorded. LOGOS_VAULT
// is a per-process override — it is how the documented scratch-vault workflow
// works, and how an MCP host config pins one server to one vault — so treating
// it as a machine-wide choice means a single `setup` run against a throwaway
// directory silently repoints every front end at it. That shipped: a scratch
// vault under an agent's job directory became the recorded pointer, and because
// the directory still existed, Recorded() kept returning it. Every command, the
// MCP server and the SessionStart hook then read an empty vault and truthfully
// reported nothing, while twenty-eight checkpoints sat in ~/logos. --vault, and
// the default, are choices someone made; an inherited environment variable is
// not.
// recorded says whether this vault became the machine's recorded pointer, and
// why not when it did not. The reason is load-bearing: the three ways to end up
// unrecorded — the environment chose the vault, the write failed, or this was a
// dry run — need three different next steps, and reporting one of them for all
// three told a user whose config directory was unwritable to "pass --vault",
// which is exactly what they had just done.
type recordOutcome int

// hostColumn is the width of the name column in setup's host report. It was
// 16 until "Copilot in VS Code" (18) pushed its arrow out of line with every
// other row's.
const hostColumn = 18

const (
	recordedHere   recordOutcome = iota // written down
	recordSkipEnv                       // LOGOS_VAULT chose it, so it is this process only
	recordFailed                        // the write was attempted and failed; the error is already printed
	recordSkipTemp                      // a temporary directory, and nobody said to record it anyway
)

func chooseVault(args []string, dryRun bool) (dir string, created bool, rec recordOutcome, err error) {
	dir = flagStr(args, "--vault", "")
	fromEnv := false
	if dir == "" {
		if v := os.Getenv("LOGOS_VAULT"); v != "" {
			dir, fromEnv = v, true
		} else {
			dir = vaultPath() // the recorded path, then ~/logos
		}
	}
	abs, err := filepath.Abs(expandHome(dir))
	if err != nil {
		return "", false, recordFailed, err
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) && flagStr(args, "--vault", "") == "" && !fromEnv && abs == vault.Pointer() {
		// Nobody asked for this directory in this run; it is the recorded vault,
		// and it is missing — usually an unmounted drive. Creating it makes an
		// empty vault at the mount path.
		return "", false, recordFailed, missingVaultError(abs)
	}
	if flagStr(args, "--vault", "") == "" && !fromEnv && looksLikeSourceTree(abs) {
		// Nobody chose this directory; it is the default, and it is a project.
		// `git clone …/logos` run in ~ lands exactly on ~/logos, and taking it
		// indexes the repository's markdown as notes and puts the user's memory
		// inside a tree `git clean` or a re-clone deletes.
		return "", false, recordFailed, fmt.Errorf("%s looks like a source checkout, not a vault — pass --vault <dir> to choose where the vault goes", abs)
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		created = true
		if !dryRun {
			// Private from the first mkdir. A vault created world-readable and
			// tightened later is a vault that was world-readable for however long
			// the user took to run `logos doctor`.
			if err := vault.MkdirPrivate(abs); err != nil {
				return "", false, recordFailed, fmt.Errorf("creating %s: %w", abs, err)
			}
		}
	}
	// A dry run reports the outcome the real run would reach, which under
	// LOGOS_VAULT is "not recorded" — the one command whose whole job is
	// previewing was promising the opposite of what followed.
	// doctor fails a recorded vault that lives under a temp root, because it
	// will be empty or gone. By then the pointer has already moved; setup is
	// the one place the check can stop it, so a temporary directory is used
	// for this run and recorded only when someone says so.
	temp := !fromEnv && health.UnderTempDir(abs)
	yes := hasFlag(args, "--yes") || hasFlag(args, "-y")
	if dryRun {
		if fromEnv {
			return abs, created, recordSkipEnv, nil
		}
		if temp && !yes {
			return abs, created, recordSkipTemp, nil
		}
		return abs, created, recordedHere, nil
	}
	if fromEnv {
		return abs, created, recordSkipEnv, nil
	}
	if temp && !yes {
		fmt.Printf("             %s is a temporary directory — it will be empty or gone\n", abs)
		if !confirmNo("             record it as this machine's vault anyway?") {
			return abs, created, recordSkipTemp, nil
		}
	}
	// Write the choice down where a front end with no shell can read it. The
	// desktop app is launched from Finder and inherits no LOGOS_VAULT, so
	// without this it can only ever find a vault at the default location.
	if err := vault.Record(abs); err != nil {
		fmt.Printf("             could not record this vault for the desktop app: %v\n", err)
		return abs, created, recordFailed, nil
	}
	return abs, created, recordedHere, nil
}

// goRunBinary reports a binary inside a go-build directory, where `go run`
// puts the executable it removes on exit. A `.test` binary lives there too,
// but it is this package's tests calling setup, not a person wiring hosts.
func goRunBinary(bin string) bool {
	if strings.HasSuffix(bin, ".test") || strings.HasSuffix(bin, ".test.exe") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(bin), "/") {
		if strings.HasPrefix(part, "go-build") {
			return true
		}
	}
	return false
}

// looksLikeSourceTree reports a directory holding a Go or npm project and no
// Logos history. A vault with sessions or memories is a vault whatever else is
// in it; a plain git repository is not enough, since notes vaults are often
// kept in git.
func looksLikeSourceTree(dir string) bool {
	for _, d := range []string{"sessions", "memories"} {
		if _, err := os.Stat(filepath.Join(dir, d)); err == nil {
			return false
		}
	}
	for _, f := range []string{"go.mod", "package.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
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
	// Pulling is Ollama's /api/pull. Every other runtime answered it with a 404
	// after the user had already said yes, so they are told what to load instead.
	canPull := p.Name == "Ollama"

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
	embed := env("LOGOS_EMBED", defaultEmbedModel)
	fmt.Printf("  embedding  %s %s\n", embed, tick(have[embed]))
	if !have[embed] {
		if !canPull {
			fmt.Printf("             load %s in %s for semantic search — logos can only pull through Ollama\n", embed, p.Name)
		} else if dryRun {
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
	fmt.Printf("             %s are optional (%s) — only `logos ask`, `voice`\n",
		strings.Join(chat, " and "), totalSize(chat))
	fmt.Println("             and the nightly rollup use them. No MCP tool does.")
	if !allModels(os.Args) {
		fmt.Println("             skipped; pass --all-models to pull them")
		return
	}
	if !canPull {
		fmt.Printf("             load %s in %s — logos can only pull through Ollama\n", strings.Join(chat, " and "), p.Name)
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
	// One updating line: a multi-gigabyte download that printed nothing until it
	// finished could not be told apart from a hang.
	last := -1
	progress := func(pct int) {
		if pct != last {
			last = pct
			fmt.Printf("\r             pulling %s … %d%% ", model, pct)
		}
	}
	if err := pullModel(baseURL, model, progress); err != nil {
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
	// The default only: a custom LOGOS_EMBED containing "embed" is not this size.
	case strings.HasPrefix(model, "nomic-embed-text"):
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

// pullTimeout bounds connecting to Ollama and waiting for it to start
// answering. Not the download: that streams for as long as the model takes.
var pullTimeout = 30 * time.Second

// pullModel asks Ollama to fetch a model. The response streams progress as
// JSON lines, passed on as a percentage of the layer being downloaded.
func pullModel(baseURL, model string, progress func(pct int)) error {
	// Ollama's native API sits alongside the OpenAI-compatible /v1 path.
	root := strings.TrimSuffix(strings.TrimSuffix(baseURL, "/"), "/v1")
	body, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return err
	}
	// The default client has no timeout, so an Ollama that accepted the
	// connection and never answered held setup forever.
	client := &http.Client{Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: pullTimeout}).DialContext,
		ResponseHeaderTimeout: pullTimeout,
	}}
	resp, err := client.Post(root+"/api/pull", "application/json", strings.NewReader(string(body)))
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
			Error     string `json:"error"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.Error != "" {
			return fmt.Errorf("%s", line.Error)
		}
		if line.Total > 0 {
			progress(int(line.Completed * 100 / line.Total))
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
	embedModel := env("LOGOS_EMBED", defaultEmbedModel)
	if found := provider.Discover(); len(found) > 0 {
		// Said before it starts: a large vault takes minutes to embed, and
		// silence for that long reads as a hang with the hosts prompt stuck
		// behind it.
		var pending int
		ix.DB.QueryRow(`SELECT COUNT(*) FROM notes n LEFT JOIN embeddings e ON e.slug = n.slug WHERE e.slug IS NULL`).Scan(&pending)
		if pending > 0 {
			fmt.Printf("  index      embedding %d %s with %s — search already works without it…\n", pending, plural(pending, "note"), embedModel)
		}
		if _, err := ix.EmbedPending(found[0].Provider, embedModel, 32); err != nil {
			fmt.Printf("  index      embedding failed: %v — search is lexical until `logos index` succeeds\n", err)
		}
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

	downgrade bool // --downgrade: replace a newer pinned copy with this older binary
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

// logosServer is the command line and environment any host — known to
// setup.Hosts() or not — needs to reach this logos and this vault. Shared by
// wireHosts, --print-config and --config so that all three describe the exact
// same server; a hand-typed config that differs from what `logos setup` itself
// would have written is a bug users would have no way to notice.
func logosServer(vault string) (setup.Server, error) {
	bin, err := selfPath()
	if err != nil {
		return setup.Server{}, err
	}
	return serverFor(bin, vault), nil
}

// executable is os.Executable, replaceable so a test can be a binary running
// from npm's npx cache.
var executable = os.Executable

func selfPath() (string, error) {
	bin, err := executable()
	if err != nil {
		return "", fmt.Errorf("could not find my own path, which the host config needs: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	return bin, nil
}

// terminalCommand is how this install is reached from a shell, for the
// commands setup suggests, with a hint when that is not simply `logos`. Setup
// used to say "logos resume <project>" regardless, and under npx, a source
// build or a release binary run from Downloads there is no logos on PATH.
func terminalCommand(self string) (cmd, hint string) {
	switch selfupdate.DetectInstall(self) {
	case selfupdate.NPX:
		return "npx @noeton/logos", "for a `logos` command, run `npm i -g @noeton/logos`"
	case selfupdate.NPMManaged:
		// npm's logos is a node shim, not this file, so it cannot be compared
		// by path; being on PATH is the whole question.
		if _, err := exec.LookPath("logos"); err == nil {
			return "logos", ""
		}
	default:
		if found, err := exec.LookPath("logos"); err == nil {
			if resolved, err := filepath.EvalSymlinks(found); err == nil && resolved == self {
				return "logos", ""
			}
		}
	}
	if strings.ContainsAny(self, " '\"$\\") {
		self = "'" + strings.ReplaceAll(self, "'", `'\''`) + "'"
	}
	// The hosts setup just wired launch this exact path, so moving the file
	// breaks every one of them unless setup rewires them to where it went.
	return self, "logos is not on your PATH — to type `logos`, move it into a directory that is (for example ~/.local/bin), then run `logos setup` again: the hosts are wired to where it is now"
}

// probeTarget is what the integration check launches, which is deliberately not
// always what the hosts launch.
//
// Under npx the wired command is `npx -y @noeton/logos mcp serve`, and running
// that here would make `logos doctor` fetch the package whenever npm's cache has
// been pruned — an egress from a command that promises nothing leaves the
// machine, and slow enough that the probe's ten-second handshake deadline
// expires first, reporting a perfectly healthy install as broken. The server
// binary is identical either way; npx only adds the fetch. So probe this binary
// and say out loud that the wired command differs, rather than quietly claiming
// to have tried it.
func probeTarget(self string, srv setup.Server) (bin string, args []string, note string) {
	// Only npx's launcher fetches; an absolute path (this binary, or Homebrew's
	// opt link to it) is probed as written.
	if srv.Bin == "npx" {
		return self, []string{"mcp", "serve"},
			fmt.Sprintf("probed this binary; hosts launch `%s %s`, which resolves the same server on demand",
				srv.Bin, strings.Join(srv.Args, " "))
	}
	return srv.Bin, srv.Args, ""
}

// serverFor is the decision logosServer makes, separated from finding this
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
	env := map[string]string{"LOGOS_VAULT": vault}
	if selfupdate.DetectInstall(bin) == selfupdate.NPX {
		return setup.Server{Bin: "npx", Args: []string{"-y", "@noeton/logos", "mcp", "serve"}, Env: env}
	}
	// Under Homebrew bin is the versioned Cellar path, which `brew upgrade`
	// deletes; the opt link follows upgrades.
	if stable := selfupdate.HomebrewStablePath(bin); stable != "" {
		bin = stable
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

// printConfigCmd is `logos setup --print-config`: the server block by hand,
// for an MCP client that is not one of the four Hosts() knows how to find or
// register. Those clients are real — MCP has more of them than this package
// will ever special-case — and until this existed, the only answer for their
// users was silence.
func printConfigCmd(args []string) error {
	vault, err := resolvedVault(args)
	if err != nil {
		return err
	}
	srv, err := logosServer(vault)
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

// configFileCmd is `logos setup --config <path>`: merge logos into a config
// file at a location logos has no built-in convention for, reusing the exact
// merge (parse-before-touch, backup-before-write, no-op-writes-nothing)
// mergeJSON already gives Claude Desktop and Cursor.
func configFileCmd(args []string, path string) error {
	vault, err := resolvedVault(args)
	if err != nil {
		return err
	}
	srv, err := logosServer(vault)
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
	// --no-hosts is for someone evaluating logos, or setting up a second vault
	// on a machine that already has one wired. Until it existed the only way to
	// create and index a vault was to also repoint every AI tool on the machine
	// at it, which is a large thing to have to accept in order to look.
	if opts.none {
		fmt.Println("\n  hosts      --no-hosts: nothing was wired")
		fmt.Println("             `logos mcp install` connects them when you are ready")
		return nil
	}
	srv, err := logosServer(vault)
	if err != nil {
		return err
	}
	// Under npx the server would be registered as `npx -y @noeton/logos`, and
	// npx asks npm's registry on every launch: offline that waited 70 seconds
	// and then failed, so there was no Logos at all without a network. The
	// binary npx is running is the whole server, so keep a copy of it outside
	// npm's cache and register that. ~/.local/bin is also a directory the
	// plugin's resolver searches, so its hooks find the copy instead of npx.
	pin := ""
	if self, err := selfPath(); err == nil && selfupdate.DetectInstall(self) == selfupdate.NPX {
		if p, err := pinnedBinary(); err == nil {
			pin = p
			srv.Bin, srv.Args = p, []string{"mcp", "serve"}
		}
	}
	bin := srv.Bin
	// `go run` builds into a go-build temp directory and deletes the binary
	// when the command exits. Every check passes during the run, then no host
	// can start the server.
	if !opts.yes && goRunBinary(bin) {
		return fmt.Errorf("%s is a `go run` build that Go deletes when this command exits — "+
			"build one that stays (`go build -o ~/.local/bin/logos ./cmd/logos` or `go install ./cmd/logos`) and run setup from it, or pass --yes to wire this one anyway", bin)
	}

	known := detectHosts()
	hosts, unmatched := setup.Only(known, opts.only)
	if len(unmatched) > 0 {
		// Named off the same list the match was made against. Reaching past the
		// seam to setup.Hosts() meant the message could list a roster the match
		// never consulted.
		return fmt.Errorf("unknown host %s — logos knows: %s",
			strings.Join(unmatched, ", "), strings.Join(setup.Names(known), ", "))
	}
	// The README offers the plugin and this command side by side, and someone
	// who ran both had logos registered twice in Claude Code. The plugin
	// already connects it, so Claude Code is left out and the reason printed.
	pluginNote := ""
	if version, ok := setup.LogosPlugin(); ok {
		kept := hosts[:0:0]
		for _, h := range hosts {
			if h.Name == "Claude Code" {
				if version != "" {
					version = " " + version
				}
				pluginNote = fmt.Sprintf("    %-*s —  already connected by the Logos plugin%s; not registered again\n", hostColumn, h.Name, version)
				continue
			}
			kept = append(kept, h)
		}
		hosts = kept
	}

	// Show the plan before touching anything. Registering logos with every AI
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
		fmt.Printf("      LOGOS_VAULT=%s\n", vault)
		if pin != "" {
			fmt.Printf("      (a copy of this binary, made there so hosts do not depend on npm's cache or the network)\n")
		}
		fmt.Println()
	}
	// Printed with or without the roster: under --yes it is the only place a
	// person learns why Claude Code was not touched.
	fmt.Print(pluginNote)
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
				fmt.Printf("    %-*s —  not installed\n", hostColumn, r.Host)
				continue
			}
			fmt.Printf("    %-*s →  %s\n", hostColumn, r.Host, r.Where)
		}
	}

	if present == 0 {
		if pluginNote != "" {
			if opts.dryRun {
				fmt.Println("\n  --dry-run: nothing was written.")
			}
			return nil
		}
		// A named host that is missing is not "no hosts found": with Cursor
		// installed, `--host codex` told the user to install Cursor and exited
		// 0, so a script took a run that wired nothing as a success.
		if len(opts.only) > 0 {
			var found []string
			for _, r := range setup.Plan(known) {
				if r.Outcome == setup.Pending {
					found = append(found, r.Host)
				}
			}
			here := "no MCP host is installed here"
			if len(found) > 0 {
				here = "found: " + strings.Join(found, ", ")
			}
			verb := "is"
			if len(hosts) > 1 {
				verb = "are"
			}
			return fmt.Errorf("%s %s not installed here (%s); nothing was wired",
				strings.Join(setup.Names(hosts), ", "), verb, here)
		}
		fmt.Printf("\n  No MCP hosts found. Install one of %s\n", strings.Join(setup.Names(known), ", "))
		fmt.Println("  and re-run `logos mcp install`.")
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
	wire := opts.yes
	if !wire {
		answer, ok := readAnswer(fmt.Sprintf("\n  wire %d host(s)? [Y/n] ", present))
		// Nobody there to ask is not a no. Exiting 0 let a provisioning script
		// (`brew install … && logos setup`) take a run that wired nothing as a
		// success; the user found out when their AI tool had no Logos.
		if !ok {
			return fmt.Errorf("nothing was wired: no terminal to ask — re-run with --yes to wire the %d host(s) found", present)
		}
		wire = answer == "" || answer == "y" || answer == "yes"
	}
	if !wire {
		fmt.Println("  skipped; re-run `logos mcp install` when you are ready")
		// The prompt cannot pick a subset, so the way to wire some of these is
		// printed with each found host's spelling, ready to trim and paste.
		var pick []string
		for _, r := range plan {
			if r.Outcome != setup.Skipped {
				pick = append(pick, "--host "+strings.ReplaceAll(strings.ToLower(r.Host), " ", "-"))
			}
		}
		fmt.Printf("  to wire only some, keep the ones you want: logos mcp install %s\n", strings.Join(pick, " "))
		return nil
	}

	if showPlan {
		fmt.Println() // separate the roster above from the outcomes below
	}
	if pin != "" {
		self, _ := selfPath()
		// An older `npx @noeton/logos@x setup` used to overwrite a newer copy,
		// quietly downgrading every host that launches it. Replacing newer with
		// older is a choice: asked with no as the default, and under --yes only
		// with --downgrade. The kept copy is still the one registered.
		theirs, _ := logosVersion(pin)
		if newerRelease(theirs, version) && !opts.downgrade &&
			(opts.yes || !confirmNo(fmt.Sprintf("    %s is logos %s, newer than this %s; replace it with the older one?", pin, theirs, version))) {
			fmt.Printf("    %-*s —  kept logos %s at %s, newer than this %s (--downgrade replaces it)\n", hostColumn, "binary", theirs, pin, version)
		} else if err := pinBinary(self, pin); err != nil {
			// Registering a path that was never written would be a host that
			// cannot start. npx still works while online, so fall back to it
			// and say what that costs.
			srv.Bin, srv.Args = "npx", []string{"-y", "@noeton/logos", "mcp", "serve"}
			bin = srv.Bin
			fmt.Printf("    %-*s ✗  could not copy logos to %s: %v\n", hostColumn, "binary", pin, err)
			fmt.Printf("    %-*s    hosts launch `npx -y @noeton/logos mcp serve` instead, which needs npm's registry to start\n", hostColumn, "")
		} else {
			fmt.Printf("    %-*s ✓  copied to %s\n", hostColumn, "binary", pin)
		}
	}
	byName := map[string]setup.Host{}
	for _, h := range hosts {
		byName[h.Name] = h
	}
	var wired, failed int
	var wiredHosts []string
	for _, r := range setup.Install(srv, hosts) {
		switch r.Outcome {
		case setup.Skipped:
			fmt.Printf("    %-*s —  not installed\n", hostColumn, r.Host)
		case setup.Failed:
			failed++
			fmt.Printf("    %-*s ✗  %v\n", hostColumn, r.Host, r.Err)
		default:
			wired++
			wiredHosts = append(wiredHosts, r.Host)
			fmt.Printf("    %-*s ✓  %s (%s)\n", hostColumn, r.Host, r.Outcome, r.Where)
			// A registration replaces what was there — `codex mcp add` drops
			// the whole previous entry, environment and all. Naming the copy is
			// what makes a wrong --vault recoverable.
			if r.Backup != "" {
				fmt.Printf("    %-*s    previous config saved as %s\n", hostColumn, "", r.Backup)
				if r.CommentsOnlyInBackup {
					fmt.Printf("    %-*s    its comments were not carried over; they are kept in that copy\n", hostColumn, "")
				}
			}
			// Through 0.4.x only: the entry 0.4 setup wrote under the old name.
			if r.Replaced {
				fmt.Printf("    %-*s    replaced the brain entry an earlier setup wrote\n", hostColumn, "")
			} else if r.ReplaceErr != nil {
				fmt.Printf("    %-*s    could not remove the brain entry an earlier setup wrote: %v\n", hostColumn, "", r.ReplaceErr)
			}
			// The README's Cursor button writes a `logos` entry, and a
			// hand-written config may use any name. Setup adding `logos` beside
			// it loads the server twice, each copy possibly on a different
			// vault, and it used to report only that it registered.
			if others := otherLogosEntries(byName[r.Host]); len(others) > 0 {
				verb, which := "runs", "that entry"
				if len(others) > 1 {
					verb, which = "run", "those entries"
				}
				fmt.Printf("    %-*s    %s also %s logos here, so it now loads twice — remove %s\n",
					hostColumn, "", strings.Join(others, ", "), verb, which)
			}
			// `claude mcp add` gives Claude Code the tools but not the
			// plugin's hooks, so nothing restores the last checkpoint when a
			// session starts. A user who never hears of the plugin never gets
			// the part of the product that works without being asked.
			if r.Host == "Claude Code" {
				fmt.Printf("    %-*s    for resume at every session start, install the Logos plugin in Claude Code:\n", hostColumn, "")
				fmt.Printf("    %-*s    /plugin marketplace add Coder8124/logos, then /plugin install logos@logos,\n", hostColumn, "")
				fmt.Printf("    %-*s    then `claude mcp remove --scope user logos` so it is not registered twice\n", hostColumn, "")
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
			fmt.Printf("    %-*s    %s\n", hostColumn, "", note)
		}
		for _, c := range integrationChecks(probeBin, probeArgs, vault) {
			if c.State == health.Failed {
				ok = false
				fmt.Printf("    %-*s ✗  %s\n", hostColumn, c.Name, c.Detail)
				if c.Fix != "" {
					fmt.Printf("    %-*s    → %s\n", hostColumn, "", c.Fix)
				}
				continue
			}
			fmt.Printf("    %-*s ✓  %s\n", hostColumn, c.Name, c.Detail)
		}
		if !ok {
			fmt.Println("\n  The hosts are configured but the server did not pass its own check.")
			fmt.Println("  `logos doctor --integration` re-runs this.")
			return nil
		}
	}

	if wired == 0 {
		// Hosts that were found and failed are not missing. Saying "No MCP
		// hosts found. Install … Cursor" under Cursor's own error contradicted
		// the line above it, and exiting 0 hid that nothing was wired.
		if failed > 0 {
			return fmt.Errorf("%d %s found, none wired — fix the error above and re-run `logos mcp install`",
				failed, plural(failed, "host"))
		}
		fmt.Printf("\n  No MCP hosts found. Install one of %s\n", strings.Join(setup.Names(known), ", "))
		fmt.Println("  and re-run `logos mcp install`.")
		return nil
	}
	// The first thing a new user sees logos do decides what they think it is.
	// "What do you remember about me?" on a fresh vault correctly answers
	// "nothing", which demonstrates an empty database rather than the product.
	//
	// Continuity is the differentiator and it works immediately, with no model
	// and no indexing: a checkpoint is markdown, and resume reads it back. So
	// the suggested first move is a handoff someone can run in thirty seconds
	// and watch survive across two different agents.
	cmd, hint := "logos", ""
	if self, err := selfPath(); err == nil {
		cmd, hint = terminalCommand(self)
	}
	tryTheHandoff(os.Stdout, wiredHosts, cmd, hint)
	return nil
}

// tryTheHandoff prints setup's closing steps. It names the hosts setup just
// wired and asks for nothing the user has to make up: an agent can fill a
// checkpoint from its own session, and resume with no project name finds the
// repository the host has open, or the most recent checkpoint. Placeholders
// ("trying X", "<project>") left a new user inventing a scenario and guessing
// the name the work was filed under.
func tryTheHandoff(w io.Writer, hosts []string, cmd, hint string) {
	restart := hosts[0]
	if len(hosts) > 1 {
		restart = strings.Join(hosts[:len(hosts)-1], ", ") + " and " + hosts[len(hosts)-1]
	}
	// Claude Desktop has no repository open, so nothing names the project for
	// it: the checkpoint is written from a host that has one, and Desktop only
	// resumes, which falls back to that most recent checkpoint.
	var inRepo []string
	for _, h := range hosts {
		if h != "Claude Desktop" {
			inRepo = append(inRepo, h)
		}
	}
	var one, two string
	switch {
	case len(inRepo) == 0:
		one = `In Claude Desktop, say: "checkpoint this under the project demo"`
		two = `In a new Claude Desktop chat, say: "resume"`
	case len(hosts) == 1:
		one = fmt.Sprintf(`In %s, in a repository you are working in, say: "checkpoint this"`, inRepo[0])
		two = fmt.Sprintf(`In a new %s session in the same repository, say: "resume this project"`, inRepo[0])
	default:
		next := hosts[0]
		if next == inRepo[0] {
			next = hosts[1]
		}
		one = fmt.Sprintf(`In %s, in a repository you are working in, say: "checkpoint this"`, inRepo[0])
		two = fmt.Sprintf(`In %s, open the same repository and say: "resume this project"`, next)
		if next == "Claude Desktop" {
			two = `In Claude Desktop, say: "resume"`
		}
	}
	fmt.Fprintf(w, "\n  Restart %s, then try the handoff — it is what logos is for:\n\n", restart)
	fmt.Fprintf(w, "    1. %s\n", one)
	fmt.Fprintf(w, "    2. %s\n\n", two)
	fmt.Fprintln(w, "  The second agent should recite what the first ruled out, without you")
	if len(inRepo) == 0 {
		fmt.Fprintf(w, "  re-explaining. From a terminal: %s resume demo\n", cmd)
	} else {
		fmt.Fprintf(w, "  re-explaining. From a terminal in that repository: %s resume\n", cmd)
	}
	if hint != "" {
		fmt.Fprintf(w, "  (%s)\n", hint)
	}
}

// setupUsage is what `logos setup --help` and `logos mcp install --help`
// print instead of running.
const setupUsage = `usage:
    logos setup [--vault DIR] [--host NAME] [--no-hosts] [--dry-run] [--yes] [--downgrade]
                                      connect logos to the AI agents on this machine
    logos setup --print-config [--vault DIR] [--format json|toml]
                                      print the server block by hand, for any MCP client logos does not wire
    logos setup --config <path> [--vault DIR]
                                      merge logos into a config file at a location logos does not know by convention
    logos mcp install [--vault DIR] [--host NAME] [--dry-run] [--yes] [--downgrade]
                                      register an existing vault with the MCP hosts found

  --vault DIR     the vault to use (default: $LOGOS_VAULT, else the recorded vault, else ~/logos)
  --host NAME     wire only this host; repeat for more
  --no-hosts      create the vault but wire nothing
  --dry-run       describe what would happen and change nothing
  --yes           accept every prompt, for scripts — including pulling a missing embedding model from Ollama
  --downgrade     under npx, replace a newer pinned logos with this older one
  --all-models    list every local model, not just the recommended ones
`

// Setup's flags, checked before it does anything. Both commands write host
// configs, and a flag they ignored used to mean a default: `--vault=~/notes`,
// `--vualt ~/notes` and a bare `--vault` each wired ~/logos into every host,
// and `--host=cursor` wired all of them.
var (
	setupBoolFlags  = []string{"--print-config", "--no-hosts", "--dry-run", "--yes", "-y", "--all-models", "--downgrade"}
	setupValueFlags = []string{"--vault", "--host", "--config", "--format"}
)

// normalizeSetupFlags splits --name=value into --name value, and refuses an
// unknown flag or a value flag with no value. A value that starts with - is
// treated as missing, because `--vault --yes` otherwise made a vault named
// --yes in the current directory.
func normalizeSetupFlags(args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
			continue
		}
		name, value, hasValue := strings.Cut(a, "=")
		switch {
		case slices.Contains(setupValueFlags, name):
			if !hasValue {
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					i++
					value, hasValue = args[i], true
				}
			}
			if !hasValue || value == "" {
				return nil, fmt.Errorf("%s needs a value, e.g. %s <value>; nothing was changed", name, name)
			}
			out = append(out, name, value)
		case slices.Contains(setupBoolFlags, name) && !hasValue:
			out = append(out, a)
		default:
			return nil, fmt.Errorf("unknown flag %s; nothing was changed. The flags are %s and %s",
				a, strings.Join(setupValueFlags, " "), strings.Join(setupBoolFlags, " "))
		}
	}
	return out, nil
}

// mcpInstallCmd is the wiring on its own, for someone who already has a vault.
func mcpInstallCmd(args []string) error {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Print(setupUsage)
		return nil
	}
	args, err := normalizeSetupFlags(args)
	if err != nil {
		return err
	}
	vault := flagStr(args, "--vault", "")
	if vault == "" {
		vault = vaultPath()
	}
	abs, err := filepath.Abs(expandHome(vault))
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("vault not found at %s — run `logos setup` first, or pass --vault", abs)
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
	answer, ok := readAnswer(prompt + " [Y/n] ")
	return ok && (answer == "" || answer == "y" || answer == "yes")
}

// confirmNo asks a question whose safe answer is no: return declines, and so
// does nobody being there.
func confirmNo(prompt string) bool {
	answer, ok := readAnswer(prompt + " [y/N] ")
	return ok && (answer == "y" || answer == "yes")
}

// readAnswer prints prompt and reads one answer, lowercased. ok is false when there
// is no answer to read.
func readAnswer(prompt string) (answer string, ok bool) {
	fmt.Print(prompt)
	// One scanner for every prompt: a scanner reads ahead, so a fresh one per
	// prompt swallowed the rest of a piped script into the first and left the
	// next prompt at EOF. Rebuilt only if stdin itself was swapped.
	if answers == nil || answersFrom != os.Stdin {
		answers, answersFrom = bufio.NewScanner(os.Stdin), os.Stdin
	}
	sc := answers
	if !sc.Scan() {
		fmt.Println("\n             no answer (not a terminal) — skipping; pass --yes to accept")
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(sc.Text())), true
}

var (
	answers     *bufio.Scanner
	answersFrom *os.File
)

// expandHome resolves a leading ~ so --vault ~/logos works from any shell.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

// otherLogosEntries names the host's registrations, other than the one setup
// just wrote, that also start logos — matched the way doctor's duplicate
// check matches them, so the two never disagree about what counts.
func otherLogosEntries(h setup.Host) []string {
	if h.List == nil {
		return nil
	}
	regs, err := h.List()
	if err != nil {
		return nil
	}
	var names []string
	for _, r := range regs {
		if r.Name == setup.Name {
			continue
		}
		if strings.Contains(r.Command, "mcp serve") || strings.HasPrefix(r.Name, "plugin:logos:") {
			names = append(names, r.Name)
		}
	}
	sort.Strings(names)
	return names
}

// pinnedBinary is where an npx setup keeps its copy of logos.
func pinnedBinary() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	name := "logos"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(home, ".local", "bin", name), nil
}

// pinBinary copies self to dst. Re-running setup through npx refreshes the
// copy, but a file there that is not logos belongs to someone else and is left
// alone. The copy is written beside dst and renamed over it, so a host
// starting mid-copy never launches half a binary.
func pinBinary(self, dst string) error {
	if _, err := os.Stat(dst); err == nil && !runsAsLogos(dst) {
		return fmt.Errorf("%s already exists and is not logos", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp-%d", dst, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// runsAsLogos is the resolver's test in plugin/bin/resolve.sh: every logos
// answers --version with "logos …". Bounded, because the file being asked
// may be any program at all.
func runsAsLogos(path string) bool {
	_, ok := logosVersion(path)
	return ok
}

// logosVersion is the version a logos at path reports, from the same
// `--version` answer runsAsLogos trusts.
func logosVersion(path string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil || !strings.HasPrefix(string(out), "logos ") {
		return "", false
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return "", true
	}
	return fields[1], true
}

// newerRelease reports whether a is a later release than b. Anything that is
// not a plain major.minor.patch — a dev build, an empty answer — compares as
// not newer, so an unreadable version never blocks a copy.
func newerRelease(a, b string) bool {
	pa, okA := releaseParts(a)
	pb, okB := releaseParts(b)
	if !okA || !okB {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func releaseParts(v string) ([3]int, bool) {
	var parts [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	fields := strings.Split(v, ".")
	if len(fields) != 3 {
		return parts, false
	}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}
