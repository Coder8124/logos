package main

import (
	"fmt"

	"github.com/Coder8124/logos/internal/ops"
)

// runLedger writes a project's ledger and says what is in it.
//
// An explicit command rather than a step of every checkpoint: the ledger reads
// a project's whole history and asks git about every file each dead end names,
// which is seconds on a long-lived project — too slow to put in front of an
// agent's checkpoint, and nothing in it goes stale between two of them that the
// checkpoints themselves do not already say.
func runLedger(args []string) error {
	project, rest := projectArg(args)
	if len(rest) > 0 {
		return fmt.Errorf("usage: logos ledger [project]")
	}
	if project == "" {
		return fmt.Errorf("no project here to write a ledger for: logos ledger <project>")
	}
	// Measured against this checkout only when it is the project's: a dead end
	// recorded at a commit in another repository has no history here, and
	// measuring it against the wrong one would say nothing changed.
	l, err := ops.WriteLedger(vaultPath(), project, repoHere(project))
	if err != nil {
		return err
	}
	fmt.Println(ops.LedgerReceipt(l))
	return nil
}
