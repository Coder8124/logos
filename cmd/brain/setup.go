package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pragun/brain/internal/index"
	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/router"
	"github.com/pragun/brain/internal/setup"
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
	if err := wireHosts(vault); err != nil {
		return err
	}
	return nil
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

	// Only the embedding model is worth blocking on. The chat tiers are used by
	// ask and the rollup; memory and continuity work without them.
	var missing []string
	for _, want := range wantedModels() {
		if !have[want] {
			missing = append(missing, want)
		}
		fmt.Printf("  %-10s %s %s\n", modelLabel(want), want, tick(have[want]))
	}
	if len(missing) == 0 {
		return
	}
	if !yes && !confirm(fmt.Sprintf("             pull %s?", strings.Join(missing, ", "))) {
		fmt.Println("             skipped; brain will use what is present")
		return
	}
	for _, m := range missing {
		fmt.Printf("             pulling %s … ", m)
		if err := pullModel(p.BaseURL, m); err != nil {
			fmt.Printf("failed: %v\n", err)
			continue
		}
		fmt.Println("done")
	}
}

// wantedModels is the embedding model plus the configured chat tiers. Reading
// them from the router config means setup pulls what this install will actually
// use rather than a hard-coded list.
func wantedModels() []string {
	out := []string{env("BRAIN_EMBED", defaultEmbedModel)}
	cfg, err := router.Load(vaultPath())
	if err != nil {
		return out
	}
	for _, t := range []router.Tier{router.T1, router.T2} {
		if tc, ok := cfg.Tiers[t.String()]; ok && tc.Model != "" && tc.BaseURL == "" {
			out = append(out, tc.Model)
		}
	}
	return out
}

func modelLabel(model string) string {
	if strings.Contains(model, "embed") {
		return "embedding"
	}
	return "model"
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
func wireHosts(vault string) error {
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

	fmt.Println("\n  hosts")
	var wired int
	for _, r := range setup.Install(srv, setup.Hosts()) {
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
	return wireHosts(abs)
}

// confirm asks a yes/no question, defaulting to yes. A non-interactive stdin
// answers yes: setup is something a person ran on purpose.
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
