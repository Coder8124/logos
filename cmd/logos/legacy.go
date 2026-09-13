package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/Coder8124/logos/internal/legacy"
)

// carryOldNames lets an install from before the rename keep running, and says
// what it carried on stderr, which the MCP stream never reads. Through 0.4.x
// only; see internal/legacy.
func carryOldNames(stderr io.Writer) {
	for _, old := range legacy.Env() {
		fmt.Fprintf(stderr, "logos: reading %s as LOGOS_%s — rename it; the old name stops working in 0.5.0\n",
			old, strings.TrimPrefix(old, "BRAIN_"))
	}
}
