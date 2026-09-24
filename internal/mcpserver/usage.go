package mcpserver

import (
	"fmt"

	"github.com/Coder8124/logos/internal/contextpack"
	"github.com/Coder8124/logos/internal/usage"
)

// ledger records one event for `logos usage` and returns what the tool should
// add to its answer: nothing when it was written, and the failure when it was
// not. A ledger that quietly stopped growing would report a total that looks
// complete and is not; the answer itself already did its job and is kept.
func (s *Server) ledger(e usage.Event) string {
	if err := usage.Record(s.vault, e); err != nil {
		return fmt.Sprintf("\n_logos could not add this to its usage ledger, so `logos usage` will not count it: %v_\n", err)
	}
	return ""
}

// ledgerPack records a rendered pack. Render fills the budget, so this must
// come after it.
func (s *Server) ledgerPack(via, project string, pack contextpack.Pack) string {
	if err := usage.RecordPack(s.vault, via, project, pack.Budget); err != nil {
		return fmt.Sprintf("\n_logos could not add this to its usage ledger, so `logos usage` will not count it: %v_\n", err)
	}
	return ""
}
