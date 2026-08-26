package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/pragun/brain/internal/eval"
	"github.com/pragun/brain/internal/eval/adapters"
	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/router"
)

// runContinuityBench runs the handoff-and-memory suite over every system that
// can be reached on this machine.
//
// Systems that are not installed are skipped with a line saying so rather than
// silently omitted, because a comparison table with a quietly missing column is
// a comparison table that flatters whoever is left.
func runContinuityBench(args []string) error {
	var (
		only    = flagStr(args, "--only", "")
		verbose = hasFlag(args, "--verbose")
		noEmbed = hasFlag(args, "--no-embed")
		bare    = hasFlag(args, "--brain-only")
		dump    = hasFlag(args, "--dump")
	)

	suite := eval.Select(eval.Suite(), only)
	if len(suite) == 0 {
		return fmt.Errorf("no scenarios match %q", only)
	}

	var embed *provider.Provider
	var model string
	if !noEmbed {
		rt, err := openRouter()
		if err != nil {
			return fmt.Errorf("%w\n(run with --no-embed to score lexical and graph retrieval only)", err)
		}
		model, err = rt.Model(router.T0)
		if err != nil {
			return err
		}
		embed = rt.Local()
	}

	fmt.Printf("· %s\n", eval.Composition(suite))
	if only != "" {
		fmt.Printf("· filtered to %q\n", only)
	}
	if embed == nil {
		fmt.Println("· no embeddings: lexical and graph retrieval only")
	} else {
		fmt.Printf("· embeddings: %s\n", model)
	}

	brain, err := adapters.NewBrain(embed, model)
	if err != nil {
		return err
	}
	defer brain.Close()

	systems := []eval.Adapter{brain}
	if !bare {
		systems = append(systems, &adapters.StaticFile{}, &adapters.Recency{}, &adapters.Dump{})
		if embed != nil {
			systems = append(systems, adapters.NewVectorRAG(embed, model, 8))
		}
		systems = append(systems, adapters.None{})

		// Third-party systems, if they are installed. Each runs in a subprocess
		// speaking the bridge protocol; see bench/adapters/.
		for _, ext := range adapters.Discover() {
			systems = append(systems, ext)
		}
	}

	var results []eval.Result
	for _, sys := range systems {
		opts := eval.Options{
			Progress: func(adapter, scenario string, done, total int) {
				fmt.Printf("\r  %-18s %3d/%d  %-36s", adapter, done, total, scenario)
			},
		}
		if dump {
			opts.Trace = func(sc eval.Scenario, resp eval.Response, score eval.Score) {
				fmt.Printf("\r%80s\r", "")
				fmt.Printf("\n╭─ %s · %s · fidelity %.0f%%\n", sys.Name(), sc.ID, score.Fidelity()*100)
				for _, line := range splitLines(resp.Text) {
					fmt.Printf("│ %s\n", line)
				}
				if resp.Err != nil {
					fmt.Printf("│ ERROR: %v\n", resp.Err)
				}
				fmt.Printf("╰─ missed: %v · leaked: %v · unsignalled: %v\n",
					score.Missed, score.Leaked, score.Unsignalled)
			}
		}
		scores, err := eval.Run(sys, suite, opts)
		fmt.Printf("\r%80s\r", "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s failed: %v\n", sys.Name(), err)
			continue
		}
		results = append(results, eval.Result{Adapter: sys.Name(), Scores: scores})
		sys.Close()
	}

	fmt.Print(eval.Report(results, verbose))
	return nil
}

// listBenchScenarios prints the suite without running it — what is being
// measured, and what brain is expected to do on each case.
func listBenchScenarios(only string) error {
	suite := eval.Select(eval.Suite(), only)
	fmt.Printf("· %s\n\n", eval.Composition(suite))
	for _, s := range suite {
		mark := "+"
		if s.Known == eval.KnownWeakness {
			mark = "−"
		}
		fmt.Printf("%s %-34s %-12s %-18s %s\n", mark, s.ID, s.Family, clipStr(s.Skill, 18), s.Why)
	}
	fmt.Println("\n+ expected strength   − known weakness")
	return nil
}

func clipStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// splitLines keeps the dump readable when a response is one long block.
func splitLines(s string) []string {
	if s == "" {
		return []string{"(empty)"}
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}
