package deadend

import (
	"database/sql"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/textmatch"
)

// before_you_try is pull: it reaches a ruling only when an agent thinks to ask.
// The moment a ruling about reader.go is most useful is the moment an agent
// opens reader.go to edit it, and nothing in that moment prompts a question.
// An edit hook can carry the ruling there — but a hook runs on every edit, so
// it can afford one indexed lookup and nothing else. Collect reads every
// checkpoint in the vault, which on a vault of two thousand checkpoints is the
// difference between a hook nobody notices and one that stalls every edit.

// A cache, not a record: every row is a file named in a Failed entry of a
// checkpoint in sessions/, so deleting the index costs the next edit hook one
// slower refresh and nothing else. Refreshed by its only reader rather than by
// each of the four checkpoint writers, so a writer added later cannot forget
// it. stamp is the checkpoint file's size and mtime: an auto checkpoint grows
// in place, and a file seen once must still be read again when it changes. A
// checkpoint naming no file keeps one row with an empty name, so it is not
// parsed again on every edit.
const atEditSchema = `CREATE TABLE IF NOT EXISTS ruling_files (
    scope  TEXT NOT NULL,
    file   TEXT NOT NULL,
    stamp  TEXT NOT NULL,
    name   TEXT NOT NULL,
    raw    TEXT NOT NULL,
    agent  TEXT NOT NULL,
    ts     INTEGER NOT NULL,
    sha    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ruling_files_name ON ruling_files(name);
CREATE INDEX IF NOT EXISTS ruling_files_file ON ruling_files(scope, file);
CREATE TABLE IF NOT EXISTS ruling_shown (
    session TEXT NOT NULL,
    path    TEXT NOT NULL,
    PRIMARY KEY (session, path)
)`

// AtPath returns the checkpoint rulings in project, and in its worktree scopes,
// that name the repository file rel, newest first.
//
// rel is matched as written ("internal/parse/reader.go") and by its base name
// for a ruling that only said "reader.go". A ruling that spelled out a
// different directory names a different file and is not returned.
//
// Working notes are not consulted: a note ruled out with `tried --ruled-out`
// reaches before_you_try and resume, and reading them here would put the
// notes table on every edit for the rarest kind of ruling.
func AtPath(db *sql.DB, vaultDir, project, rel string) ([]Ruling, error) {
	rel = filepath.ToSlash(strings.TrimPrefix(rel, "./"))
	if db == nil || strings.TrimSpace(project) == "" || rel == "" {
		return nil, nil
	}
	if _, err := db.Exec(atEditSchema); err != nil {
		return nil, err
	}
	scopes, err := scopesOf(vaultDir, project)
	if err != nil {
		return nil, err
	}
	for _, s := range scopes {
		if err := refresh(db, vaultDir, s); err != nil {
			return nil, err
		}
	}
	if len(scopes) == 0 {
		return nil, nil
	}

	marks := strings.TrimSuffix(strings.Repeat("?,", len(scopes)), ",")
	args := []any{}
	for _, s := range scopes {
		args = append(args, s)
	}
	base := path.Base(rel)
	args = append(args, rel, base)
	rows, err := db.Query(`SELECT scope, file, name, raw, agent, ts, sha FROM ruling_files
		WHERE scope IN (`+marks+`) AND (name = ? OR name = ?)
		ORDER BY ts DESC, file DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Ruling
	seen := map[string]bool{}
	for rows.Next() {
		var scope, file, name, raw, agent, sha string
		var ts int64
		if err := rows.Scan(&scope, &file, &name, &raw, &agent, &ts, &sha); err != nil {
			return nil, err
		}
		// A bare name only stands for rel when the ruling gave no directory;
		// "cmd/reader.go" is not internal/parse/reader.go.
		if name != rel && strings.Contains(name, "/") {
			continue
		}
		slug := filepath.ToSlash(filepath.Join(session.CheckpointDir, session.SafeScope(scope), strings.TrimSuffix(file, ".md")))
		if seen[slug+"\x00"+raw] {
			continue // one ruling naming both reader.go and its path is one ruling
		}
		seen[slug+"\x00"+raw] = true
		rec := ParseRecord(raw)
		out = append(out, Ruling{
			Text: rec.Route, Project: scope, Agent: agent, When: ts, Slug: slug,
			Source: FromCheckpoint, Commit: sha, Record: rec,
			Stale: PossiblySuperseded(rec.Scope, ts, time.Now()),
		})
	}
	return out, rows.Err()
}

// scopesOf is project and the worktree scopes filed under it — the same two
// levels session.Scopes walks, without walking every other project.
func scopesOf(vaultDir, project string) ([]string, error) {
	project = session.SafeScope(project)
	root := filepath.Join(vaultDir, session.CheckpointDir, filepath.FromSlash(project))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []string{project}
	if strings.Contains(project, "/") {
		return out, nil // already a worktree scope; there is no level below it
	}
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, project+"/"+e.Name())
		}
	}
	return out, nil
}

// refresh brings one scope's rows up to date with its directory: parses the
// checkpoint files it has not seen or that changed since, and drops the rows
// of files that are gone. One transaction, so a hook killed partway leaves the
// cache as it was rather than half a file's rows.
func refresh(db *sql.DB, vaultDir, scope string) error {
	dir := filepath.Join(vaultDir, session.CheckpointDir, filepath.FromSlash(scope))
	onDisk, err := stamps(dir)
	if err != nil {
		return err
	}
	known, err := knownStamps(db, scope)
	if err != nil {
		return err
	}
	return apply(db, dir, scope, onDisk, known)
}

// stamps is each checkpoint file in dir with its size and mtime.
func stamps(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	onDisk := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !session.IsCheckpointFile(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // gone between the listing and the stat; the next refresh agrees
		}
		onDisk[e.Name()] = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
	}
	return onDisk, nil
}

// knownStamps is each checkpoint file the cache holds rows for in scope, with
// the stamp it had when they were filed.
func knownStamps(db *sql.DB, scope string) (map[string]string, error) {
	known := map[string]string{}
	rows, err := db.Query(`SELECT DISTINCT file, stamp FROM ruling_files WHERE scope = ?`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var file, stamp string
		if err := rows.Scan(&file, &stamp); err != nil {
			return nil, err
		}
		known[file] = stamp
	}
	return known, rows.Err()
}

// apply files what changed between known and onDisk. known is read outside
// the transaction, so it can be out of date by the time apply runs: another
// hook may have filed the same files in between. Every write here replaces a
// file's rows rather than adding to them, so that costs a repeated parse and
// never a duplicate.
func apply(db *sql.DB, dir, scope string, onDisk, known map[string]string) error {
	var stale, fresh []string
	for file, stamp := range known {
		if onDisk[file] != stamp {
			stale = append(stale, file)
		}
	}
	for file, stamp := range onDisk {
		if known[file] != stamp {
			fresh = append(fresh, file)
		}
	}
	if len(stale) == 0 && len(fresh) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, file := range stale {
		if onDisk[file] != "" {
			continue // changed, not gone: replaced below with the rest of fresh
		}
		if _, err := tx.Exec(`DELETE FROM ruling_files WHERE scope = ? AND file = ?`, scope, file); err != nil {
			return err
		}
	}
	for _, file := range fresh {
		// Deleted even when known had no rows for it — see apply's comment.
		if _, err := tx.Exec(`DELETE FROM ruling_files WHERE scope = ? AND file = ?`, scope, file); err != nil {
			return err
		}
		raw, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			continue // unreadable now; no row, so the next refresh tries again
		}
		c := session.ParseCheckpoint(string(raw))
		named := 0
		for _, f := range c.Failed {
			if session.IsPlaceholder(f) {
				continue
			}
			flat := textmatch.Flatten(f)
			for _, name := range gitstate.NamedFiles(flat) {
				if _, err := tx.Exec(`INSERT INTO ruling_files (scope, file, stamp, name, raw, agent, ts, sha)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
					scope, file, onDisk[file], name, flat, c.Agent, c.TS, c.Git.Commit); err != nil {
					return err
				}
				named++
			}
		}
		if named == 0 {
			if _, err := tx.Exec(`INSERT INTO ruling_files (scope, file, stamp, name, raw, agent, ts, sha)
				VALUES (?, ?, ?, '', '', '', 0, '')`, scope, file, onDisk[file]); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// FirstShowing reports whether this is the first time in session that a ruling
// was shown for rel, and records that it now has been. One line per file per
// session: an agent editing reader.go twenty times needs the ruling once, and a
// line repeated on every edit is read as noise and then not read at all.
//
// A cursor, not a record — losing it shows a ruling once more. An empty session
// cannot be told apart from any other, so it always counts as a first showing.
func FirstShowing(db *sql.DB, sessionID, rel string) (bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return true, nil
	}
	if _, err := db.Exec(atEditSchema); err != nil {
		return false, err
	}
	res, err := db.Exec(`INSERT OR IGNORE INTO ruling_shown (session, path) VALUES (?, ?)`, sessionID, rel)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
