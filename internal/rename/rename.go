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

	// Merged and Collisions are only meaningful when Run was called with
	// merge=true: Merged is how many entries moved from the old project's
	// session directory into the existing target directory, and Collisions
	// is how many of those shared a filename with something already there
	// and were renamed rather than overwriting it.
	Merged     int
	Collisions int
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
//
// merge changes what happens when sessions/<to>/ already exists. Without it,
// that is refused (see the comment at the Stat below) because an ordinary
// rename onto an existing project raises a question this function does not
// answer: whose checkpoint from the same minute is authoritative. With it,
// the question does not need answering, because nothing is discarded — every
// entry under sessions/<from>/ is moved into sessions/<to>/, and a filename
// collision is resolved by keeping both under distinct names rather than
// picking a winner. This is the fix for a project whose folder was renamed
// after it already had history: the old and new names are not two projects in
// conflict, they are one project's history that got split, and merge heals
// that without asking the user to choose which half to keep.
func Run(db *sql.DB, vaultDir, from, to string, dryRun, merge bool) (Result, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	var res Result
	if from == "" || to == "" {
		return res, fmt.Errorf("both the old and new project names are required")
	}
	if from == to {
		return res, fmt.Errorf("%q is already the name", to)
	}
	// Both names become a path under sessions/, so both have to survive being
	// one. Rejecting up front beats writing half a rename and discovering the
	// target is unusable.
	//
	// `from` is checked for the same reason as `to`, and it is the more
	// dangerous of the two: it is joined onto the vault path and then read,
	// rewritten and os.Rename'd. A "../.." in it walks straight out of the
	// vault, and this function would happily rewrite frontmatter in someone's
	// home directory and then move the whole directory inside the vault. The
	// name is user input on a command line and a tool argument over MCP, so it
	// is never trusted for a path.
	if err := checkName(from); err != nil {
		return res, fmt.Errorf("old name: %w", err)
	}
	if err := checkName(to); err != nil {
		return res, fmt.Errorf("new name: %w", err)
	}

	res.Dir = filepath.Join(vaultDir, "sessions", from)
	res.NewDir = filepath.Join(vaultDir, "sessions", to)
	if _, err := os.Stat(res.NewDir); err == nil && !merge {
		// Merging two projects is a different operation with different
		// questions (whose checkpoint is authoritative when both have one from
		// the same minute?). Refusing is honest; silently interleaving is not.
		// --merge answers those questions by keeping both sides rather than
		// picking one, so it is allowed to proceed past this check.
		return res, fmt.Errorf("sessions/%s already exists — rename it or pick another name, or pass --merge to combine both histories", to)
	}

	n, err := rewriteCheckpoints(res.Dir, from, to, dryRun)
	if err != nil {
		return res, err
	}
	res.Checkpoints = n

	// Move the directory if there is one, not if we happened to rewrite
	// something inside it. A project can own a session directory without owning
	// a checkpoint yet — working notes live there too, and so does anything a
	// user filed beside their history — and gating the move on the rewrite
	// count left all of it behind under a name that no longer exists, while the
	// index had already moved on. The two then disagreed, and `brain index`
	// resolved the disagreement in favour of the stale copy.
	if merge {
		// os.Rename onto an existing, non-empty directory fails on every
		// platform this targets, so a plain directory move cannot express a
		// merge at all — it has to move entry by entry, which is also what
		// makes filename collisions visible and handleable one at a time
		// instead of the whole rename failing on the first one.
		moved, collisions, err := mergeDirInto(res.Dir, res.NewDir, dryRun)
		res.Merged, res.Collisions = moved, collisions
		if err != nil {
			// Reported, not swallowed: whatever moved before the failure stays
			// moved (recoverable — those files are already under the new name
			// and rerunning the merge only touches what is left in Dir), and
			// the caller is told exactly that rather than being handed a
			// result that looks like it finished.
			return res, fmt.Errorf("merging sessions/%s into sessions/%s: %w", from, to, err)
		}
	} else if !dryRun {
		if _, err := os.Stat(res.Dir); err == nil {
			if err := os.Rename(res.Dir, res.NewDir); err != nil {
				return res, fmt.Errorf("moving sessions/%s: %w", from, err)
			}
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

// mergeDirInto moves every entry from src into dst, recursively (a worktree
// sub-scope is a directory of its own and needs the same treatment as the
// checkpoints beside it), without ever letting one entry silently replace
// another of the same name. A name already present in dst is not proof the
// incoming file is a duplicate — it is two different checkpoints that happen
// to share a timestamp-and-agent filename, one from each half of a split
// history — so the incoming one is renamed instead of clobbering what is
// there, and the count of times that happened is returned so the caller can
// say so (a merge that resolved five collisions and reported only "moved 12
// files" would hide the one fact a user most needs to know here).
//
// dryRun still walks and counts, but writes nothing, matching Run's existing
// contract that a dry run's numbers are the real ones.
func mergeDirInto(src, dst string, dryRun bool) (moved, collisions int, err error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	if !dryRun {
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return 0, 0, err
		}
	}
	for _, e := range entries {
		sp := filepath.Join(src, e.Name())
		dp := filepath.Join(dst, e.Name())
		if e.IsDir() {
			m, c, err := mergeDirInto(sp, dp, dryRun)
			moved += m
			collisions += c
			if err != nil {
				return moved, collisions, err
			}
			if !dryRun {
				// Only succeeds once every entry below it has actually moved
				// out, so a non-empty leftover here is a sign something under
				// it failed silently — and os.Remove refusing to delete a
				// non-empty directory is exactly the safety net that catches
				// that rather than losing the leftover files.
				os.Remove(sp)
			}
			continue
		}
		if _, statErr := os.Stat(dp); statErr == nil {
			dp = disambiguate(dp)
			collisions++
		}
		moved++
		if dryRun {
			continue
		}
		if err := os.Rename(sp, dp); err != nil {
			return moved, collisions, fmt.Errorf("moving %s: %w", sp, err)
		}
	}
	if !dryRun {
		os.Remove(src) // best-effort: only empties, never errors the merge
	}
	return moved, collisions, nil
}

// disambiguate finds a filename beside path that nothing already occupies, by
// inserting "-<n>" before the extension. Starts at 2 ("the second file with
// this name") rather than 1, so a lone survivor never carries a suffix that
// implies a sibling that isn't there.
func disambiguate(path string) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d%s", base, i, ext)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}

// checkName rejects a name that cannot be one directory under sessions/.
//
// A separator is the one that matters most, twice over: sessions/<project>/
// <worktree> is a real scope this product uses, so a name containing "/" would
// forge one — and the name is joined onto the vault path, so "../.." would
// escape the vault entirely.
//
// A whitelist would be tighter still, but it would also reject names that work
// today: internal/session's safe() already allows any Unicode letter, and a
// project legitimately named in Japanese or Greek must keep working. So this
// rejects the constructs that mean something to a filesystem, and leaves the
// rest alone.
func checkName(name string) error {
	if name == "" {
		return fmt.Errorf("a project name cannot be empty")
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("a project name cannot contain a path separator: %q", name)
	}
	// Not just "..": a leading dot hides the directory, and a name that is all
	// dots is a relative path however many there are.
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("a project name cannot start with a dot: %q", name)
	}
	// A NUL truncates the path at the syscall boundary, so a name carrying one
	// is not the name the caller sees in the error message.
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("a project name cannot contain a null byte")
	}
	// filepath.Join cleans as it joins, so the only way to be sure the name is
	// one path element is to ask whether it survives being treated as one.
	if filepath.Clean(name) != name || filepath.Base(name) != name {
		return fmt.Errorf("a project name must be a single path element: %q", name)
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
			// A vault that has never stored a memory has no table yet, which is
			// not a rename failure — and reporting it as one is worse than
			// unhelpful here, because the vault half of the rename has already
			// happened by the time this runs.
			if missingTable(err) {
				continue
			}
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
		if missingTable(err) {
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

// missingTable reports whether an error is a vault that simply has not created
// this part of the index yet.
func missingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}
