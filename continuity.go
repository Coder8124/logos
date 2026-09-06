package brain

import "github.com/Coder8124/brain/internal/session"

// Note records one line of progress. Cheap and meant to be called often — after
// a decision, a dead end, a surprising discovery. Notes stay uncommitted until
// Checkpoint folds them into a durable record, so use them freely rather than
// saving everything for the end.
func (b *Brain) Note(project, text string) error {
	_, err := session.AddNote(b.ix.DB, project, b.agent, text)
	return err
}

// Notes returns a project's uncommitted progress — work that happened but was
// never written down properly, including anything left by an agent that died
// mid-task.
func (b *Brain) Notes(project string) ([]Note, error) {
	return session.Uncommitted(b.ix.DB, project)
}

// Checkpoint commits where you stopped to a markdown note in the vault and
// returns its slug.
//
// Call it before finishing a session, not after. Anything omitted is lost, and
// the field that matters most is Failed: approaches that did not work are the
// expensive knowledge, and without them the next agent repeats them.
func (b *Brain) Checkpoint(c Checkpoint) (string, error) {
	if c.Agent == "" {
		c.Agent = b.agent
	}
	if err := session.Commit(b.ix.DB, b.ix.Vault, &c); err != nil {
		return "", err
	}
	return c.Slug, nil
}

// History returns a project's checkpoints, newest first.
func (b *Brain) History(project string, n int) ([]Checkpoint, error) {
	return session.History(b.ix.Vault, project, n)
}

// Projects lists the projects that have at least one checkpoint.
func (b *Brain) Projects() ([]string, error) {
	return session.Projects(b.ix.Vault)
}
