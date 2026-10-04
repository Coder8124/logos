package main

import (
	"fmt"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/router"
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

// runBenchQA runs the end-to-end LongMemEval pipeline — retrieve, answer,
// grade — the metric actually comparable to another memory system's
// published LongMemEval score, unlike runBench's recall@k.
func runBenchQA(path string, n int, hybrid bool, depth int) error {
	rt, err := openRouter()
	if err != nil {
		return err
	}
	embed, _ := rt.Model(router.T0)
	genModel, err := rt.ModelFor(router.T1, false)
	if err != nil {
		return err
	}
	judgeModel, err := rt.ModelFor(router.T1, true)
	if err != nil {
		return err
	}

	mode := "hybrid (vector+BM25)"
	if !hybrid {
		mode = "vector-only"
	}
	fmt.Printf("· LongMemEval QA accuracy over %d instances · %s · gen %s · judge %s\n", n, mode, genModel, judgeModel)
	results, err := memory.RunLongMemEvalQA(rt.Local(), embed, genModel, judgeModel, path, n, hybrid, depth, func(done, total int) {
		fmt.Printf("\r  %d/%d …", done, total)
	})
	if err != nil {
		return err
	}
	fmt.Printf("\r%40s\r", "")

	fmt.Printf("\n%-26s %8s %6s\n", "category", "accuracy", "n")
	for _, r := range results {
		fmt.Printf("%-26s %7.1f%% %6d\n", r.Category, r.Accuracy()*100, r.N)
	}
	return nil
}
