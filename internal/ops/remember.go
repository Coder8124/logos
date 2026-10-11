package ops

import (
	"database/sql"
	"fmt"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/procedure"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/secret"
)

type RememberRequest struct {
	Memory memory.Memory
	Embed  *provider.Provider // nil dedups by exact text
	Model  string
}

type RememberResult struct {
	Receipt memory.Receipt `json:"receipt"`
	Outcome Outcome        `json:"outcome"`
}

// Remember stores a memory. The error is for a write refused before anything
// was stored. A write the cache took and the vault did not is a result, not
// an error: the memory exists, recall returns it, and its id is the one handle
// anyone has to re-save or forget it — so the receipt comes back with the
// failure in its Outcome instead of being dropped for it.
func Remember(db *sql.DB, req RememberRequest) (RememberResult, error) {
	m := req.Memory
	// A procedure earns its slot by naming what goes wrong without it — the
	// trap test. Refuse here, with the reason, rather than storing a
	// convention that will never be flagged as one again: a rejected write
	// must not come back looking like a stored one.
	if m.Kind == memory.Procedure {
		if err := procedure.Validate(procedure.ParseRecord(m.Text)); err != nil {
			return RememberResult{}, err
		}
	}
	r, err := memory.Store(db, req.Embed, req.Model, &m)
	if err != nil && r.ID == 0 && r.Ref == 0 {
		return RememberResult{}, err
	}
	res := RememberResult{Receipt: r}
	if said := secret.Summary(r.Redactions); said != "" {
		res.Outcome.say(said + ".")
	}
	res.Outcome.say(QueueAdoption(r.QueueRestored, r.QueueRejected))
	if err != nil {
		res.Outcome.fail(fmt.Sprintf("memory #%d: %v", stored(r), err))
	}
	return res, nil
}

// stored is the memory a receipt is about: the new row, or the one a
// restatement reinforced.
func stored(r memory.Receipt) int64 {
	if r.Ref != 0 {
		return r.Ref
	}
	return r.ID
}
