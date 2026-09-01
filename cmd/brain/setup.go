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
	"github.com/Coder8124/brain/internal/setup"
)

// brain setup — the one command between cloning this and an agent answering
// from your vault.
//
// Every step reports and then continues. A missing model runtime does not stop
// the hosts being wired, because retrieval without a model still works and a
// half-configured machine is worse than a configured one with a warning on it.

func setupCmd(args []string) error {
	yes := hasFlag(args, "--yes") || hasFlag(args, "-y")

	vault, created, err := chooseVault(args)
	if err != nil {
		return err
	}
	fmt.Printf("  vault      %s", vault)
	if created {
		fmt.Print("   (created)")
	}
	fmt.Println()

	// Everything below wants the vault to be the one we just chose, whatever
	// the environment said when the process started.
	os.Setenv("BRAIN_VAULT", vault)

	checkRuntime(yes)
	indexVault(vault)
	if err := wireHosts(vault, wireOptsFrom(args)); err != nil {
		return err
	}
	return nil
}

// wireOptsFrom reads the wiring flags shared by `setup` and `mcp install`.
func wireOptsFrom(args []string) wireOpts {
	return wireOpts{
		only:   flagStrs(args, "--host"),
		dryRun: hasFlag(args, "--dry-run"),
		yes:    hasFlag(args, "--yes") || hasFlag(args, "-y"),
	}
}

// chooseVault resolves where the vault lives and makes sure it exists.
func chooseVault(args []string) (dir string, created bool, err error) {
	dir = flagStr(args, "--vault", "")
	if dir == "" {
		dir = vaultPath() // BRAIN_VAULT, else the ~/brain default
	}
	abs, err := filepath.Abs(expandHome(dir))
	if err != nil {
		return "", false, err
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", false, fmt.Errorf("creating %s: %w", abs, err)
		}
		created = true
	}
	return abs, created, nil
}

// checkRuntime reports the local model runtime and offers to pull what is
// missing. A machine with no runtime is told what to install and left working:
// lexical retrieval and the whole continuity surface need no model at all.
func checkRuntime(yes bool) {
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
		if yes || confirm(fmt.Sprintf("             pull %s (%s)? adds semantic search",
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
	dryRun bool     // --dry-run: show the plan and change nothing
	yes    bool     // --yes: do not prompt
}

func wireHosts(vault string, opts wireOpts) error {
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not find my own path, which the host config needs: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}

	srv := setup.Server{
		Bin:  bin,
		Args: []string{"mcp", "serve"},
		// Absolute, and always written: a host launches the server from a
		// directory nobody chose, and a relative vault would silently resolve
		// somewhere the user will never look.
		Env: map[string]string{"BRAIN_VAULT": vault},
	}

	hosts, unmatched := setup.Only(setup.Hosts(), opts.only)
	if len(unmatched) > 0 {
		return fmt.Errorf("unknown host %s — brain knows: %s",
			strings.Join(unmatched, ", "), strings.Join(setup.Names(setup.Hosts()), ", "))
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
	for _, r := range plan {
		if r.Outcome == setup.Skipped {
			fmt.Printf("    %-16s —  not installed\n", r.Host)
			continue
		}
		fmt.Printf("    %-16s →  %s\n", r.Host, r.Where)
	}

	if present == 0 {
		fmt.Println("\n  No MCP hosts found. Install Claude Code, Claude Desktop, Cursor or")
		fmt.Println("  Codex and re-run `brain mcp install`.")
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

	fmt.Println()
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
		for _, c := range health.Integration(bin, vault) {
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
