package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/Coder8124/logos/internal/legacy"
	"github.com/Coder8124/logos/internal/vault"
)

// carryOldNames lets an install from before the rename keep running, and says
// what it carried on stderr, which the MCP stream never reads. Through 0.4.x
// only; see internal/legacy.
func carryOldNames(stderr io.Writer) {
	for _, old := range legacy.Env() {
		fmt.Fprintf(stderr, "logos: reading %s as LOGOS_%s — rename it; the old name stops working in 0.5.0\n",
			old, strings.TrimPrefix(old, "BRAIN_"))
	}
	// After Env, so a BRAIN_VAULT already counts as a choice for this process.
	if notice := legacy.Vault(); notice != "" {
		fmt.Fprintf(stderr, "logos: %s\n", notice)
	}
	// After Vault, which may have just changed which vault this is.
	if notice := legacy.StateDir(vault.Path()); notice != "" {
		fmt.Fprintf(stderr, "logos: %s\n", notice)
	}
}
