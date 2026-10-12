package memory

import (
	"database/sql"
	"errors"
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

		// The fingerprint is unique across the whole vault, so the corrected
		// text may already be some memory's. Settled here, before anything is
		// written: insertLocked's answer to a duplicate is to reinforce it,
		// which for a refusal below would leave a "reinforced" line in the log
		// of a correction that never happened.
		fp := fingerprint(m.Text)
		var owner, ownerSuperseded int64
		var ownerQueued bool
		var ownerKind, ownerProject string
		var ownerPin int
		if db.QueryRow("SELECT id, superseded, quarantined, kind, project, pin FROM memories WHERE fingerprint = ?", fp).
			Scan(&owner, &ownerSuperseded, &ownerQueued, &ownerKind, &ownerProject, &ownerPin) == nil {
			switch {
			case owner == id:
				return fmt.Errorf("memory #%d already says that", id)
			case ownerSuperseded != 0:
				// Correcting back to wording that was itself replaced earlier.
				// That row is out of the vault already, so a rebuild would free
				// the fingerprint anyway; freeing it now makes the live store
				// agree, instead of reinforcing a memory recall never returns.
				if _, err := db.Exec("UPDATE memories SET fingerprint = NULL WHERE id = ?", owner); err != nil {
					return err
				}
				owner = 0
			case ownerQueued:
				return fmt.Errorf("memory #%d already says that and is waiting for review — accept it with `logos review`, then forget #%d", owner, id)
			case Kind(ownerKind) != m.Kind || ownerProject != project:
				// Folding into it would retire this project's version of the
				// fact in favour of another scope's — the move the inheritance
				// above exists to prevent — and its file is not under our lock.
				return fmt.Errorf("memory #%d (%s, %s) already says that word for word — word the correction differently, or forget #%d", owner, ownerKind, scopeName(ownerProject), id)
			}
		}

		if owner > 0 {
			// Same kind, same project: this is the memory the user means, and
			// the correction corroborates it rather than adding a twin.
			res.ID, res.Existing = owner, true
			db.Exec("UPDATE memories SET uses = uses + 1 WHERE id = ?", owner)
			logEvent(db, owner, EvReinforced, m.Text, 0)
		} else {
			var vec []byte
			if p != nil {
				if vecs, err := p.Embed(embedModel, []string{m.Text}); err == nil && len(vecs) == 1 {
					vec = floatsToBlob(vecs[0])
				}
			}
			correctRace(db, m.Text)
			// insertMemory, not insertLocked: on a duplicate the latter
			// reinforces it, and a correction refused here would leave that
			// reinforcement in the log — the trace the check above exists to
			// avoid.
			dup, err := insertMemory(db, m, vec)
			if err != nil {
				return err
			}
			if dup > 0 {
				// Another kind's writer stored this exact text between the check
				// above and the insert.
				return fmt.Errorf("memory #%d was stored with the same text a moment ago — run the correction again", dup)
			}
			logEvent(db, m.ID, EvCreated, m.Text, 0)
			res.ID, ownerPin = m.ID, PinNone
		}
		// From here the new row exists, so whatever fails, the kind file is
		// still written: a row only the index holds is forgotten by the next
		// reconcile, and the caller is told about a memory that then vanishes.
		err = carryLocked(db, id, res.ID, oldText, pin, ownerPin)
		return errors.Join(err, flushLocked(db, m.Kind))
	})
	return res, err
}

// correctRace runs between Correct's fingerprint check and its insert. A test
// stores the same text there, as another kind's writer could.
var correctRace = func(db *sql.DB, text string) {}

// carryLocked is the half of Correct after the replacement exists: the pin,
// then the old memory retired in its favour.
func carryLocked(db *sql.DB, old, replacement int64, oldText string, pin, replacementPin int) error {
	// An always-pin was the user's judgement about the fact, not the wording;
	// dropping it would quietly take a standing instruction out of every
	// context pack the moment it was fixed. An exclusion is the opposite: a
	// judgement about the wording being replaced, and carried over it would
	// hide the correction from recall while the caller reports it stored. A
	// correction folded into an excluded memory says that text is true now, so
	// it lifts the exclusion for the same reason.
	want := replacementPin
	switch {
	case pin == PinAlways:
		want = PinAlways
	case replacementPin == PinNever:
		want = PinNone
	}
	if want != replacementPin {
		if _, err := db.Exec("UPDATE memories SET pin = ? WHERE id = ?", want, replacement); err != nil {
			return err
		}
	}
	if _, err := db.Exec("UPDATE memories SET superseded = 1, superseded_by = ? WHERE id = ?", replacement, old); err != nil {
		return err
	}
	logEvent(db, old, EvSuperseded, oldText, replacement)
	return nil
}

func scopeName(project string) string {
	if project == "" {
		return "global"
	}
	return project
}

// KindOf is the kind memory id was stored as, which a correction of it keeps.
func KindOf(db *sql.DB, id int64) (Kind, error) {
	var kind string
	err := db.QueryRow("SELECT kind FROM memories WHERE id = ?", id).Scan(&kind)
	return Kind(kind), err
}
