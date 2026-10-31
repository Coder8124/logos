package engine

import "fmt"

// Search retrieves vault notes, fusing lexical and vector rankings.
func (b *Logos) Search(query string, k int) ([]Hit, error) {
	if k <= 0 {
		k = 8
	}
	if !b.Embedded() {
		return b.ix.LexicalSearch(query, k)
	}
	return b.ix.HybridSearch(b.embed, b.embedModel, query, k)
}

// Ask retrieves and then answers in prose, citing what it used. Requires a chat
// model; without one it returns an error rather than a guess.
func (b *Logos) Ask(question string, k int) (string, []Hit, error) {
	if b.rt == nil || b.chatModel == "" {
		return "", nil, fmt.Errorf("ask needs a local model runtime; none was found")
	}
	return b.ix.Ask(b.embed, b.embedModel, b.chatModel, question, k, 0)
}

// Index reconciles the vault into the cache: notes, embeddings and memories.
// Call it after writing files into the vault by other means, or on a watcher.
func (b *Logos) Index() (SyncReport, error) {
	rep, err := b.ix.Sync()
	if err != nil {
		return rep, err
	}
	if b.Embedded() {
		if _, err := b.ix.EmbedPending(b.embed, b.embedModel, 32); err != nil {
			return rep, err
		}
	}
	// The memory import's conflict copies join the walk's in the report, so
	// an embedder sees every file set aside: SyncMemories dropped them, and a
	// copy nobody is told about is a sync conflict nobody resolves.
	_, _, edits, err := b.ix.SyncMemoryEdits(b.embed, b.embedModel)
	rep.ConflictCopies = append(rep.ConflictCopies, edits.SetAside...)
	rep.ConflictCopies = append(rep.ConflictCopies, edits.Orphaned...)
	return rep, err
}
