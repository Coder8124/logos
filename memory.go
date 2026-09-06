package brain

import "github.com/Coder8124/brain/internal/memory"

// Remember stores something durable about the user and reports what happened:
// whether it created a fact or corroborated one already held. Near-identical
// statements reinforce rather than duplicate.
func (b *Brain) Remember(text string, kind Kind) (Receipt, error) {
	if kind == "" {
		kind = Fact
	}
	return memory.Store(b.ix.DB, b.embed, b.embedModel, &Memory{
		Text: text, Kind: kind, Salience: 0.7, Source: "sdk",
	})
}

// Recall retrieves what is known about the user relevant to a query.
func (b *Brain) Recall(query string, k int) ([]Memory, error) {
	if k <= 0 {
		k = 5
	}
	return memory.Recall(b.ix.DB, b.embed, b.embedModel, query, k)
}

// Memories returns everything currently held.
func (b *Brain) Memories() ([]Memory, error) { return memory.All(b.ix.DB) }

// Forget deletes a memory by id.
func (b *Brain) Forget(id int64) error { return memory.Forget(b.ix.DB, id) }
