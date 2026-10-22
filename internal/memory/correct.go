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
	if secret.NothingLeft(m.Text) {
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
		// Asked again under the lock: another agent may have corrected or
		// forgotten it since the caller last looked.
		var oldText, project, oldUid string
		var superseded, by int64
		var quarantined bool
		var pin int
		err := db.QueryRow("SELECT text, project, superseded, superseded_by, quarantined, pin, uid FROM memories WHERE id = ?", id).
			Scan(&oldText, &project, &superseded, &by, &quarantined, &pin, &oldUid)
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
			// the correction corroborates it rather than adding a twin. It
			// still gets the forward link — the fact that owner now holds
			// what used to be true at id is as real a supersession as the
			// insert-a-new-row case below, and a reader of owner's own file
			// should be able to say what it replaced without the database.
			res.ID, res.Existing = owner, true
			if _, err := db.Exec("UPDATE memories SET uses = uses + 1, supersedes = ? WHERE id = ?", oldUid, owner); err != nil {
				return err
			}
			logEvent(db, owner, EvReinforced, m.Text, 0)
			// Written now, not left to the shared exportMemoryFile call below:
			// carryLocked still has to touch id's row (and maybe owner's pin),
			// and every statement between this update and that later write is
			// a chance to crash with the uses bump and the forward link sitting
			// in the index with nothing in memories/<kind>.md to show for them.
			// The shared call after carryLocked still runs and overwrites this —
			// that second write is what picks up a pin change of carryLocked's.
			if err := exportMemoryFile(db, m.Kind, owner); err != nil {
				return err
			}
		} else {
			var vec []byte
			if p != nil {
				if vecs, err := p.Embed(embedModel, []string{m.Text}); err == nil && len(vecs) == 1 {
					vec = floatsToBlob(vecs[0])
				}
			}
			// Correct calls insertMemory directly, not Store, so the one place
			// that ordinarily assigns Uid (see Memory.Uid and computeUID) never
			// runs for this row — leaving it permanently uid-less: no file of
			// its own to write (exportMemoryFile skips a uid-less row), and no
			// forward link for whoever corrects it next. Same guard Store uses.
			if m.Uid == "" {
				m.Uid = computeUID(m.Kind, m.Text, m.Remote)
			}
			// Set before the insert, not patched on after: insertMemory's own
			// INSERT is the one write that creates this row, and the forward
			// link belongs in it from the start the same way Uid does.
			m.Supersedes = oldUid
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
		// From here the new row exists, so whatever fails, the vault still
		// gets both halves of it: the superseded memory's own file removed,
		// the replacement's written, and the kind digest refreshed — a row
		// only the index holds is a memory the next `rm -rf .logos` loses.
		err = carryLocked(db, id, res.ID, oldText, pin, ownerPin)
		err = errors.Join(err, exportMemoryFile(db, m.Kind, id), exportMemoryFile(db, m.Kind, res.ID))
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

// ProjectOf is the project memory id is filed under, "" for a global one.
func ProjectOf(db *sql.DB, id int64) (string, error) {
	var project string
	err := db.QueryRow("SELECT project FROM memories WHERE id = ?", id).Scan(&project)
	return project, err
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
