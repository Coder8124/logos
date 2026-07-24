// Command brain is the CLI front end. The same packages back the Wails app, so
// there is one engine rather than two.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pragun/brain/internal/capture"
	"github.com/pragun/brain/internal/capture/sources"
	"github.com/pragun/brain/internal/index"
	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/router"
)

const (
	defaultEmbedModel = "nomic-embed-text"
	defaultChatModel  = "qwen3.6"
	// Default first-run reach. Deliberately not "everything" — see PollOnce.
	defaultBackfillDays = 7
)

func usage() {
	fmt.Fprintf(os.Stderr, `brain — local-first second brain

USAGE
    brain mode [secretary|tutor|business]   switch or show the active flavor
    brain tutor [diagnostic <subject> | study|quiz <topic> | cards|review | screen on|off | help]
    brain business [read|analyze|verify <file> | forecast <file> <col> | agent <goal> | ...]
    brain brief                       what the secretary thinks you should know now
    brain weekly                      Sunday executive briefing: your week in review
    brain jot <thought>               braindump: capture and auto-file a thought\n    brain memory [add <fact>|forget <id>|log|history <id>|graph]   persistent memory\n    brain projects | project <name>   auto-detected projects and their dossiers\n    brain mcp serve                   serve local memory to MCP hosts (Claude Desktop, Cursor…)\n    brain record [--name X] [--no-video]   record a study session into notes\n    brain graph [focus] [--hops N] [--similar]   memory graph around a note\n    brain loop [add|done|drop]        manage open loops (commitments)
    brain doctor [--probe]            list runtimes and tiers; --probe loads each model
    brain key set|rm <ref>            manage API keys in the macOS keychain
    brain index [--watch]             sync vault into the cache and embed
    brain ask <question…>             retrieve and answer from the vault
    brain search <query…>             retrieve only, no generation
    brain capture [--daemon] [--backfill-days N]
                                      pull episodic events
    brain timeline [--verbose]        today's activity
    brain rollup [--date YYYY-MM-DD] [--dry-run]
                                      distil a day into a note and proposals
    brain review [--all]              accept or reject queued proposals
    brain routines [--days N] [--propose]
                                      mine recurring patterns from the timeline
    brain prune [days]                drop raw events past the retention window

ENV
    BRAIN_VAULT   path to the vault (default ./vault)
    BRAIN_MODEL   chat model (default %s)
    BRAIN_EMBED   embed model (default %s)
    BRAIN_REPOS   colon-separated repos to mine for commits
`, defaultChatModel, defaultEmbedModel)
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
	case cmd == "doctor":
		err = doctor(hasFlag(args, "--probe"))
	case cmd == "key":
		err = keyCmd(args)
	case cmd == "index":
		err = runIndex(hasFlag(args, "--watch"))
	case cmd == "search" && rest != "":
		err = search(rest)
	case cmd == "ask" && rest != "":
		err = ask(rest)
	case cmd == "capture":
		err = runCapture(hasFlag(args, "--daemon"), flagInt(args, "--backfill-days", defaultBackfillDays))
	case cmd == "timeline":
		err = timeline(hasFlag(args, "--verbose"))
	case cmd == "rollup":
		err = runRollup(flagStr(args, "--date", ""), hasFlag(args, "--dry-run"))
	case cmd == "review":
		err = runReview(hasFlag(args, "--all"))
	case cmd == "mode":
		err = modeCmd(args)
	case cmd == "tutor":
		err = tutorCmd(args)
	case cmd == "business":
		err = businessCmd(args)
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
	case cmd == "record":
		err = runRecord(flagStr(args, "--name", ""), hasFlag(args, "--no-video"))
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

func vaultPath() string { return env("BRAIN_VAULT", "vault") }

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

func openIndex() (*index.Index, error) {
	v := vaultPath()
	if _, err := os.Stat(v); err != nil {
		return nil, fmt.Errorf("vault not found at %s — set BRAIN_VAULT", v)
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
	found := provider.Discover()
	if len(found) == 0 {
		return fmt.Errorf("no local runtime responding on any known port")
	}
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

	p, err := findProvider()
	if err != nil {
		return err
	}
	embedModel := env("BRAIN_EMBED", defaultEmbedModel)

	pass := func() error {
		rep, err := ix.Sync()
		if err != nil {
			return err
		}
		embedded, err := ix.EmbedPending(p, embedModel, 32)
		if err != nil {
			return err
		}
		notes, _ := ix.NoteCount()
		edges, _ := ix.EdgeCount()
		fmt.Printf("+%d ~%d -%d =%d · embedded %d · %d notes, %d edges\n",
			rep.Added, rep.Updated, rep.Removed, rep.Unchanged, embedded, notes, edges)
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

	p, err := findProvider()
	if err != nil {
		return err
	}

	hits, err := ix.HybridSearch(p, env("BRAIN_EMBED", defaultEmbedModel), query, 8)
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

	// The tutor screen watcher runs on its own slow cadence and only does
	// anything in tutor mode with screen notes on — it re-checks each tick, so
	// switching flavors mid-run just works.
	screen, screenErr := newScreenWatcher(ix, ix.Vault, scratchDir(ix.Vault))
	if screenErr == nil && screen.enabled() {
		fmt.Println("· tutor screen notes active")
	}
	screenTicker := time.NewTicker(3 * time.Minute)
	defer screenTicker.Stop()

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

		case <-screenTicker.C:
			if screenErr == nil {
				if msg := screen.tick(); msg != "" {
					fmt.Printf("· %s — `brain review`\n", msg)
				}
			}
		}
	}
}

// tutorHelp captures the screen and gives coaching for whatever the student is
// stuck on. The CLI counterpart to the app's idle-help overlay.
func tutorHelp() error {
	ix, err := openIndex()
	if err != nil {
		return err
	}
	defer ix.Close()
	rt, err := openRouter()
	if err != nil {
		return err
	}

	text, err := sources.CaptureScreenText(scratchDir(ix.Vault))
	if err != nil {
		return fmt.Errorf("couldn't read the screen (Screen Recording permission?): %w", err)
	}
	if !tutorLooksStudious(text) {
		fmt.Println("nothing studious on screen to help with right now.")
		return nil
	}

	guidance, err := tutorHelpText(rt, text)
	if err != nil {
		return err
	}
	fmt.Printf("\n%s\n", strings.TrimSpace(guidance))
	return nil
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
