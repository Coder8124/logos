// Package rename moves every trace of a project from one name to another, so
// that renaming a project is an ordinary edit rather than an event that costs
// you your history.
//
// Why this exists: a project name is derived from the folder you are standing
// in (see internal/scope), and folders get renamed. Before this, renaming one
// silently forked the work — the new name had no checkpoints, the old name had
// all of them, and nothing anywhere said so. The workaround was to pin the old
// name with a .logos-project marker, which is a fine answer to "these two
// names are the same work" and a bad answer to "I changed my mind about the
// name". Changing a name should not be a thing you avoid doing.
//
// A name is written down in five places, and a rename that misses one leaves
// history split in a way that is harder to notice than a clean break:
//
//	sessions/<name>/            the vault directory checkpoints are filed under
//	  ...*.md frontmatter       project: <name>, and a [[<name>]] relation
//	memories/<kind>.md          project=<name> in each line's trailing comment
//	activity/*.jsonl            "project":"<name>" on each event
//	index.db                    memories.project, memory_log.project,
//	                            sessions.project
//
// The vault is rewritten first and the index second, in that order and never
// the reverse: the vault is the truth and the index is a cache that `brain
// index` can rebuild from it. A crash between the two leaves a stale cache,
// which is recoverable; a crash the other way would leave an index pointing at
// a name the vault no longer uses, which is not.
package rename

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coder8124/brain/internal/vault"
)

// Result counts what a rename touched, so the caller can report it rather than
// claiming success in the abstract. A rename that reports "0 checkpoints" when
// the user expected forty is the failure worth surfacing, and it is only
// visible if the numbers are.
type Result struct {
	Checkpoints int // vault session notes rewritten
	Memories    int // memory lines rewritten in memories/*.md
	Events      int // activity log events rewritten
	Rows        int // index rows updated
	Dir         string
	NewDir      string
}

// Empty reports whether the rename found nothing at all under the old name —
// almost always a typo in the name, and worth saying so instead of printing a
// row of zeroes that reads like success.
func (r Result) Empty() bool {
	return r.Checkpoints == 0 && r.Memories == 0 && r.Events == 0 && r.Rows == 0
}

// Run renames project `from` to `to` across the vault and then the index.
//
// dryRun does every read and no write, returning the same counts the real run
// would produce. It exists because this rewrites files in the user's vault,
// and "show me what you would touch" has to be available without asking the
// user to trust a description of it.
func Run(db *sql.DB, vaultDir, from, to string, dryRun bool) (Result, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	var res Result
	if from == "" || to == "" {
		return res, fmt.Errorf("both the old and new project names are required")
	}
	if from == to {
		return res, fmt.Errorf("%q is already the name", to)
	}
	// A name becomes a directory under sessions/, so it has to survive being
	// one. Rejecting up front beats writing half a rename and discovering the
	// target is unusable.
	if err := checkName(to); err != nil {
		return res, err
	}

	res.Dir = filepath.Join(vaultDir, "sessions", from)
	res.NewDir = filepath.Join(vaultDir, "sessions", to)
	if _, err := os.Stat(res.NewDir); err == nil {
		// Merging two projects is a different operation with different
		// questions (whose checkpoint is authoritative when both have one from
		// the same minute?). Refusing is honest; silently interleaving is not.
		return res, fmt.Errorf("sessions/%s already exists — rename it or pick another name; this does not merge two projects", to)
	}

	n, err := rewriteCheckpoints(res.Dir, from, to, dryRun)
	if err != nil {
		return res, err
	}
	res.Checkpoints = n

	if res.Checkpoints > 0 && !dryRun {
		if err := os.Rename(res.Dir, res.NewDir); err != nil {
			return res, fmt.Errorf("moving sessions/%s: %w", from, err)
		}
	}

	if res.Memories, err = rewriteMemories(filepath.Join(vaultDir, "memories"), from, to, dryRun); err != nil {
		return res, err
	}
	if res.Events, err = rewriteActivity(filepath.Join(vaultDir, "activity"), from, to, dryRun); err != nil {
		return res, err
	}
	if db != nil {
		if res.Rows, err = rewriteIndex(db, from, to, dryRun); err != nil {
			return res, err
		}
	}
	return res, nil
}

// checkName rejects a name that cannot be a directory under sessions/. A
// separator is the one that matters: sessions/<project>/<worktree> is a real
// scope this product uses, and a name containing "/" would forge one.
func checkName(name string) error {
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("a project name cannot contain a path separator: %q", name)
	}
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return fmt.Errorf("a project name cannot start with a dot: %q", name)
	}
	return nil
}

// rewriteCheckpoints updates the frontmatter of every note under the project's
// session directory, including any worktree sub-directories, which are part of
// the same project and move with it.
//
// The directory move itself is the caller's, and happens after: rewriting in
// place and then moving means a failure partway leaves files under the old
// name with the new name inside them, which `brain index` reconciles. Moving
// first would leave the reverse.
func rewriteCheckpoints(dir, from, to string, dryRun bool) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			// A linked worktree is a sub-scope of the project, not a project of
			// its own, so it is renamed with its parent rather than separately.
			n, err := rewriteCheckpoints(path, from, to, dryRun)
			if err != nil {
				return count, err
			}
			count += n
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return count, err
		}
		out, changed := retitle(string(raw), from, to)
		if !changed {
			continue
		}
		count++
		if dryRun {
			continue
		}
		if err := vault.WriteAtomic(path, []byte(out)); err != nil {
			return count, err
		}
	}
	return count, nil
}

// retitle rewrites the project name where a checkpoint note records it: the
// `project:` frontmatter key and the `[[<name>]]` relation that links the
// checkpoint to its project note.
//
// Deliberately not a blind string replacement over the whole file. A
// checkpoint's body is prose written by an agent, and it may well contain the
// old name in a sentence — "renamed brain to logos" is exactly the sentence a
// checkpoint about this operation would contain. Rewriting that would falsify
// the record while claiming to move it.
func retitle(raw, from, to string) (string, bool) {
	lines := strings.Split(raw, "\n")
	changed := false
	inFront := len(lines) > 0 && strings.TrimSpace(lines[0]) == "---"
	for i := 1; i < len(lines) && inFront; i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			break
		}
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "project: "+from || trimmed == "project: "+quoteIfNeeded(from):
			lines[i] = strings.Replace(line, trimmed, "project: "+to, 1)
			changed = true
		case strings.Contains(line, "[["+from+"]]"):
			lines[i] = strings.ReplaceAll(line, "[["+from+"]]", "[["+to+"]]")
			changed = true
		}
	}
	return strings.Join(lines, "\n"), changed
}

func quoteIfNeeded(s string) string {
	if strings.ContainsAny(s, ":#'\"") {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return s
}

// rewriteMemories updates the project= field in the trailing HTML comment on
// each memory line (see internal/memory/vaultstore.go, which writes it).
//
// Field-scoped for the same reason retitle is: a memory's text is prose, and a
// memory whose text mentions the old name is a fact about the old name, not a
// mis-filed record.
func rewriteMemories(dir, from, to string, dryRun bool) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return count, err
		}
		lines := strings.Split(string(raw), "\n")
		fileChanged := false
		for i, line := range lines {
			out, ok := replaceField(line, "project=", from, to)
			if ok {
				lines[i] = out
				count++
				fileChanged = true
			}
		}
		if !fileChanged || dryRun {
			continue
		}
		if err := vault.WriteAtomic(path, []byte(strings.Join(lines, "\n"))); err != nil {
			return count, err
		}
	}
	return count, nil
}

// replaceField swaps `key=from` for `key=to` in one line, matching the whole
// value rather than a prefix of it. Prefix matching would rename "logos" and
// "logos-www" together, which is the class of bug that makes people distrust a
// bulk edit.
func replaceField(line, key, from, to string) (string, bool) {
	i := strings.Index(line, key)
	if i < 0 {
		return line, false
	}
	rest := line[i+len(key):]
	end := strings.IndexAny(rest, " \t")
	if end < 0 {
		end = len(rest)
	}
	// A trailing --> closes the comment and is not part of the value.
	if j := strings.Index(rest[:end], "-->"); j >= 0 {
		end = j
	}
	if rest[:end] != from {
		return line, false
	}
	return line[:i] + key + to + rest[end:], true
}

// rewriteActivity updates the project field on each JSONL event. Parsed and
// re-encoded rather than string-replaced, because an event carries a free-text
// summary — often a prompt the user typed — and a prompt that mentions the old
// project name must not be edited by a rename.
func rewriteActivity(dir, from, to string, dryRun bool) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := os.Open(path)
		if err != nil {
			return count, err
		}
		var out strings.Builder
		fileChanged := false
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			line := sc.Text()
			var ev map[string]any
			// A line that will not parse is passed through untouched. The log is
			// append-only and written by hooks under time pressure; a torn write
			// is data to preserve, not a reason to fail a rename.
			if err := json.Unmarshal([]byte(line), &ev); err == nil {
				if p, ok := ev["project"].(string); ok && p == from {
					ev["project"] = to
					if b, err := json.Marshal(ev); err == nil {
						line = string(b)
						count++
						fileChanged = true
					}
				}
			}
			out.WriteString(line)
			out.WriteString("\n")
		}
		scanErr := sc.Err()
		f.Close()
		if scanErr != nil {
			return count, scanErr
		}
		if !fileChanged || dryRun {
			continue
		}
		if err := vault.WriteAtomic(path, []byte(out.String())); err != nil {
			return count, err
		}
	}
	return count, nil
}

// rewriteIndex updates the cache. Runs last, and its failure is recoverable by
// `brain index`, which is why the vault goes first.
//
// The sessions table is scoped by a name that may carry a worktree
// sub-scope — "logos/feature-x" — so it matches the name and anything beneath
// it, while memories and the log hold the bare project and match exactly.
func rewriteIndex(db *sql.DB, from, to string, dryRun bool) (int, error) {
	stmts := []struct {
		count, update string
		args          []any
	}{
		{`SELECT COUNT(*) FROM memories WHERE project = ?`,
			`UPDATE memories SET project = ? WHERE project = ?`, []any{from}},
		{`SELECT COUNT(*) FROM memory_log WHERE project = ?`,
			`UPDATE memory_log SET project = ? WHERE project = ?`, []any{from}},
	}
	total := 0
	for _, s := range stmts {
		var n int
		if err := db.QueryRow(s.count, s.args...).Scan(&n); err != nil {
			return total, err
		}
		total += n
		if n == 0 || dryRun {
			continue
		}
		if _, err := db.Exec(s.update, to, from); err != nil {
			return total, err
		}
	}

	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE project = ? OR project LIKE ? || '/%'`, from, from).Scan(&n); err != nil {
		// A vault that has never held a session has no table yet, and that is
		// not a rename failure.
		if strings.Contains(err.Error(), "no such table") {
			return total, nil
		}
		return total, err
	}
	total += n
	if n > 0 && !dryRun {
		if _, err := db.Exec(
			`UPDATE sessions SET project = ? || substr(project, ?) WHERE project = ? OR project LIKE ? || '/%'`,
			to, len(from)+1, from, from); err != nil {
			return total, err
		}
	}
	return total, nil
}
