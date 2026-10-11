package ops

import (
	"database/sql"
	"fmt"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/secret"
)

type CorrectRequest struct {
	ID     int64
	Memory memory.Memory // the text that is true now; kind and project come from ID
	Embed  *provider.Provider
	Model  string
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
