package ops

import (
	"database/sql"
	"fmt"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/procedure"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/secret"
)

type CorrectRequest struct {
	ID     int64
	Memory memory.Memory // the text that is true now; kind and project come from ID
	Embed  *provider.Provider
	Model  string
	// DirFor names the repository a project lives in, or "". The correction
	// is anchored where the project of the memory it replaces stands — not
	// where the caller does, which may be a different project's checkout.
	DirFor func(project string) string
}

type CorrectResult struct {
	Correction memory.Correction `json:"correction"`
	Outcome    Outcome           `json:"outcome"`
}

// Correct replaces a memory with what is true now. As with Remember, the error
// is for a correction refused before anything changed; one the cache made and
// the vault did not is a result with the failure in its Outcome, because the
// new id is the handle anyone needs to retry it.
func Correct(db *sql.DB, req CorrectRequest) (CorrectResult, error) {
	m := req.Memory
	// The correction is stored as the old memory's kind, so a procedure's must
	// pass remember's check: one without a route and a trap is never matched by
	// before_you_try, and would retire the procedure that was.
	if kind, err := memory.KindOf(db, req.ID); err == nil && kind == memory.Procedure {
		if err := procedure.Validate(procedure.ParseRecord(m.Text)); err != nil {
			return CorrectResult{}, err
		}
	}
	if req.DirFor != nil {
		if project, err := memory.ProjectOf(db, req.ID); err == nil {
			Anchor(&m, req.DirFor(project))
		}
	}
	c, err := memory.Correct(db, req.Embed, req.Model, req.ID, &m)
	if err != nil && c.ID == 0 {
		return CorrectResult{}, err
	}
	res := CorrectResult{Correction: c}
	if said := secret.Summary(c.Redactions); said != "" {
		res.Outcome.say(said + ".")
	}
	if err != nil {
		res.Outcome.fail(fmt.Sprintf("memory #%d: %v", c.ID, err))
	}
	return res, nil
}
