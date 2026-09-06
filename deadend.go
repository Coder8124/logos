package brain

import (
	"github.com/Coder8124/brain/internal/deadend"
	"github.com/Coder8124/brain/internal/session"
)

// Tried reports whether an approach has already been ruled out, searching every
// dead end recorded across the whole vault — other projects included, and
// findings left in working notes by agents that no longer exist.
//
// Call this before proposing a solution, especially when it seems obvious:
// obvious approaches are the ones already attempted. Pass the project being
// worked on so rulings from elsewhere can be flagged as possibly not
// transferring. An empty result means no record, which is not the same as
// approval.
func (b *Brain) Tried(approach, project string) ([]Ruling, error) {
	return deadend.Check(b.ix.Vault, b.ix.DB, b.embed, b.embedModel, approach, project, 6)
}

// Why reports what was being decided when a file was worked on: the decisions
// taken and the approaches ruled out while it was being touched, newest first.
//
// The complement to `git blame`, which answers who and when and cannot answer
// why. Matching on the path is deliberately loose — an agent records whatever
// path it had in hand and a caller asks with whatever they have — so a bare
// filename or a partial path both resolve.
//
// It reads markdown from the vault, so it needs no model and no index. limit of
// 0 means every match.
func (b *Brain) Why(file string, limit int) ([]Mention, error) {
	return session.Touching(b.ix.Vault, file, limit)
}

// Explain renders the result of Tried as prose to put in front of a model,
// taking the same approach string so the output can quote what was proposed. A
// recorded failure is evidence, not a veto, and the wording says so. An empty
// slice renders as an explicit "no record", which is worth showing: silence and
// approval are different answers.
func Explain(approach string, rulings []Ruling) string {
	return deadend.Render(approach, rulings)
}
