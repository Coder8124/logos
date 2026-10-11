package memory

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/secret"
)

// A Correction says what Correct did: which memory now holds the truth, and
// which one it replaced.
type Correction struct {
	ID       int64 `json:"id"`
	Replaced int64 `json:"replaced"`
	// Existing is set when the corrected text was already an active memory, so
	// the old one was retired in its favour instead of a twin being written.
	// "Corrected — #12 is new" would be false, and the caller says so.
	Existing   bool               `json:"existing,omitempty"`
	Redactions []secret.Redaction `json:"redactions,omitempty"`
}

// Correct replaces memory id with m in one step: m is stored, and the old
// memory is marked superseded by it.
//
// Forget-then-remember was the only way to do this before, and it is two
// mistakes. Forgetting erases what was believed, so the history can no longer
// say "this used to be 8080"; and remembering the new fact on its own runs it
// through the contested check, which finds the very memory being corrected
// and queues the correction for review. A person who said "that is wrong, it
// is now X" has already reviewed it. So the new memory skips semantic dedup and
// the contested check, and the old one is kept on record, out of recall.
//
// The link survives deleting the index without a vault format change: the
// superseded memory leaves memories/<kind>.md, and the superseded event in
// memories/log.md — the one ImportLog replays — names its replacement.
func Correct(db *sql.DB, p *provider.Provider, embedModel string, id int64, m *Memory) (Correction, error) {
	res := Correction{Replaced: id}
	if strings.TrimSpace(m.Text) == "" {
		return res, fmt.Errorf("no correction given for memory #%d", id)
	}
	// Masked first, for the reason Store gives (#209).
	m.Text, res.Redactions = secret.Mask("memory", m.Text)
	if strings.Trim(strings.ReplaceAll(m.Text, secret.Marker, ""), " \t\n.,;:'\"`") == "" {
		return res, ErrOnlySecret
	}
	var kind string
	if err := db.QueryRow("SELECT kind FROM memories WHERE id = ?", id).Scan(&kind); err != nil {
		if err == sql.ErrNoRows {
			return res, fmt.Errorf("no memory #%d", id)
		}
		return res, err
	}
	// The correction is about the same thing as the memory it replaces, so it
	// belongs where that memory was: same kind file, same project. Taking the
	// caller's would let a correction from a host with no project set move a
	// project's fact into the global scope.
	m.Kind = Kind(kind)
	m.Quarantined = false
	if m.Created == 0 {
		m.Created = time.Now().Unix()
	}
	if m.Salience == 0 {
		m.Salience = 0.5
	}
	if m.Confidence == 0 {
		m.Confidence = defaultConfidence(m.Source)
	}

	err := withKind(db, m.Kind, func() error {
		if err := reconcileLocked(db, m.Kind); err != nil {
			return err
		}
		// Asked again under the lock, after the reconcile: another agent may
		// have corrected or forgotten it since, or the user deleted its line.
		var oldText, project string
		var superseded, by int64
		var quarantined bool
		var pin int
		err := db.QueryRow("SELECT text, project, superseded, superseded_by, quarantined, pin FROM memories WHERE id = ?", id).
			Scan(&oldText, &project, &superseded, &by, &quarantined, &pin)
		switch {
		case err == sql.ErrNoRows:
			return fmt.Errorf("no memory #%d", id)
		case err != nil:
			return err
		case superseded != 0 && by > 0:
			return fmt.Errorf("memory #%d was already replaced by #%d — correct that one instead", id, by)
		case superseded != 0:
			return fmt.Errorf("memory #%d was already replaced", id)
		case quarantined:
			// A proposal nobody accepted is not a belief to correct; superseding
			// it would also strand it in the review queue file.
			return fmt.Errorf("memory #%d is waiting for review — accept or reject it with `logos review`", id)
		}
		m.Project = project

		fp := fingerprint(m.Text)
		var owner, ownerSuperseded int64
		if db.QueryRow("SELECT id, superseded FROM memories WHERE fingerprint = ?", fp).Scan(&owner, &ownerSuperseded) == nil {
			if owner == id {
				return fmt.Errorf("memory #%d already says that", id)
			}
			// Correcting back to wording that was itself replaced earlier. That
			// row is out of the vault already, so a rebuild would free the
			// fingerprint anyway; freeing it now makes the live store agree,
			// instead of reinforcing a memory recall will never return.
			if ownerSuperseded != 0 {
				if _, err := db.Exec("UPDATE memories SET fingerprint = NULL WHERE id = ?", owner); err != nil {
					return err
				}
			}
		}

		var vec []byte
		if p != nil {
			if vecs, err := p.Embed(embedModel, []string{m.Text}); err == nil && len(vecs) == 1 {
				vec = floatsToBlob(vecs[0])
			}
		}
		rec, err := insertLocked(db, m, vec)
		if err != nil {
			return err
		}
		switch {
		case rec.Outcome == EvReinforced && rec.StillQueued:
			return fmt.Errorf("memory #%d already says that and is waiting for review — accept it with `logos review`, then forget #%d", rec.Ref, id)
		case rec.Outcome == EvReinforced:
			res.Existing = true
		case pin != PinNone:
			// The pin was the user's judgement about the fact, not the wording;
			// dropping it would quietly take a standing instruction out of every
			// context pack the moment it was fixed.
			if _, err := db.Exec("UPDATE memories SET pin = ? WHERE id = ?", pin, rec.ID); err != nil {
				return err
			}
		}
		res.ID = rec.ID
		if _, err := db.Exec("UPDATE memories SET superseded = 1, superseded_by = ? WHERE id = ?", res.ID, id); err != nil {
			return err
		}
		logEvent(db, id, EvSuperseded, oldText, res.ID)
		return flushLocked(db, m.Kind)
	})
	return res, err
}
