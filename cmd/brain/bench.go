package main

import (
	"fmt"

	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/router"
)

// runBench evaluates the memory's retrieval recall at several k against a
// LongMemEval file, in one embedding pass.
func runBench(path string, n int, hybrid bool) error {
	rt, err := openRouter()
	if err != nil {
		return err
	}
	embed, _ := rt.Model(router.T0)

	mode := "hybrid (vector+BM25)"
	if !hybrid {
		mode = "vector-only"
	}
	fmt.Printf("· LongMemEval retrieval recall over %d instances · %s\n", n, mode)
	results, err := memory.RunLongMemEval(rt.Local(), embed, path, n, hybrid, func(done, total int) {
		fmt.Printf("\r  %d/%d …", done, total)
	})
	if err != nil {
		return err
	}
	fmt.Printf("\r%40s\r", "")

	// header
	fmt.Printf("\n%-26s", "category")
	for _, k := range memory.Ks {
		fmt.Printf(" %6s", fmt.Sprintf("@%d", k))
	}
	fmt.Printf("   n\n")
	for _, r := range results {
		fmt.Printf("%-26s", r.Category)
		for _, k := range memory.Ks {
			fmt.Printf(" %5.1f%%", r.RecallAt(k)*100)
		}
		fmt.Printf("   %d\n", r.N)
	}
	return nil
}
