package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
)

// checkRuntime reports the local model runtime and offers to pull what is
// missing. A machine with no runtime hears nothing about one: lexical retrieval
// and the whole continuity surface need no model, and telling a coding-agent
// user to install Ollama made a tool that needs no configuring look like it did.
// dryRun turns every offer into a description. `--dry-run --yes` used to be a
// combination that downloaded models — several gigabytes, from a command whose
// last line says nothing was written.
func checkRuntime(yes, dryRun bool) {
	found := provider.Discover()
	if len(found) == 0 {
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
