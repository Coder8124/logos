package session

import (
	"database/sql"
	"sort"
	"strings"
	"time"
)

// The continuity report: "is this actually working, across the whole vault?"
//
// Everything upstream of this file answers one project's question — has this
// one checkpointed, is this one abandoned. Nobody could ask the vault-wide
// version of it: which projects hand off cleanly, which have gone quiet, and
// which never got a checkpoint at all. That last case is the one worth naming —
// a project with a hundred notes and zero checkpoints does not show up in any
// per-project view, because there is no checkpoint to go look at. It has to be
// found by listing every project that exists and asking of each one, and that
// is what this does. No new storage: it reads exactly what History and
// FindAbandoned already read, aggregated.

// QuietAfter is how long since the last checkpoint before a project reads as
// having gone quiet rather than simply between sessions. Two weeks: long
// enough to survive a normal gap between working stretches on a side project,
// short enough that a project going genuinely stale is caught within a month.
const QuietAfter = 14 * 24 * time.Hour

// ProjectContinuity is one project's continuity health.
type ProjectContinuity struct {
	Project string
	// Checkpoints is how many exist in total; LastCheckpoint and LastAgent
	// describe the most recent one. LastCheckpoint is 0 when there has never
	// been one — a project with notes or sessions but no handoff yet.
	Checkpoints    int
	LastCheckpoint int64
	LastAgent      string
	// Uncommitted is working notes recorded since the last checkpoint (or ever,
	// if there is none) that have not been folded into one yet.
	Uncommitted int
	// Abandoned is open sessions that have gone silent past AbandonAfter — see
	// abandon.go. A project can have both a healthy checkpoint history and an
	// abandoned session sitting alongside it; the two are independent signals.
	Abandoned int
}

// Quiet reports whether this project's continuity has gone stale: no
// checkpoint at all, or none within QuietAfter.
func (pc ProjectContinuity) Quiet() bool {
	if pc.LastCheckpoint == 0 {
		return true
	}
	return time.Since(time.Unix(pc.LastCheckpoint, 0)) > QuietAfter
}

// Continuity reports one project's continuity health.
func Continuity(db *sql.DB, vaultDir, project string) (ProjectContinuity, error) {
	pc := ProjectContinuity{Project: project}

	// n<=0 means unlimited — see History — because a count needs every
	// checkpoint, not the most recent few.
	all, err := History(vaultDir, project, 0)
	if err != nil {
		return pc, err
	}
	pc.Checkpoints = len(all)
	if len(all) > 0 {
		pc.LastCheckpoint = all[0].TS
		pc.LastAgent = all[0].Agent
	}

	uncommitted, err := Uncommitted(db, project)
	if err != nil {
		return pc, err
	}
	pc.Uncommitted = len(uncommitted)

	abandoned, err := FindAbandonedInProject(db, project, AbandonAfter)
	if err != nil {
		return pc, err
	}
	pc.Abandoned = len(abandoned)

	return pc, nil
}

// AllContinuity reports on every project with any continuity footprint at
// all — a checkpoint, or a session, committed or not — sorted most recently
// active first, with projects that have never checkpointed last. That order
// puts what needs attention at the bottom of a normal-length vault rather than
// requiring a second pass to find it.
//
// The project set comes from two places because either alone misses a case
// that matters: Projects(vaultDir) lists whoever has a checkpoint, and misses
// a project an agent is actively working that has not committed yet — which is
// exactly the project this report most needs to surface. The sessions table
// lists whoever has a session, open or closed, and misses a project someone
// imported checkpoints for without ever running brain through it locally.
func AllContinuity(db *sql.DB, vaultDir string) ([]ProjectContinuity, error) {
	names := map[string]bool{}

	top, err := Projects(vaultDir)
	if err != nil {
		return nil, err
	}
	for _, p := range top {
		names[p] = true
	}

	rows, err := db.Query(`SELECT DISTINCT project FROM sessions`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			rows.Close()
			return nil, err
		}
		// A session's project may be "kestrel/feature-x" — a worktree scope, a
		// folder inside kestrel's, not a project of its own. See safeScope's
		// doc comment; the report rolls worktrees up into their parent so a
		// vault with three linked worktrees does not read as three projects.
		if root, _, ok := strings.Cut(scope, "/"); ok {
			names[root] = true
		} else {
			names[scope] = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	out := make([]ProjectContinuity, 0, len(names))
	for name := range names {
		pc, err := Continuity(db, vaultDir, name)
		if err != nil {
			return nil, err
		}
		out = append(out, pc)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		// Never-checkpointed projects sort last regardless of how recently
		// they were active — a project with no checkpoint needs attention no
		// matter how fresh its uncommitted notes are.
		if (a.LastCheckpoint == 0) != (b.LastCheckpoint == 0) {
			return a.LastCheckpoint != 0
		}
		if a.LastCheckpoint != b.LastCheckpoint {
			return a.LastCheckpoint > b.LastCheckpoint
		}
		return a.Project < b.Project
	})
	return out, nil
}
