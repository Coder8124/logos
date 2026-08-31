// Package gitstate reads what the repository was, so a checkpoint does not have
// to rely on being told.
//
// Everything else brain records is model-initiated: an agent decides to call
// checkpoint, and if it does not, nothing happens. That is the single largest
// reliability gap in the product — the core promise depends on a host model
// remembering a tool at the one moment it is least likely to.
//
// Git state is the part that needs no cooperation. Branch, commit, dirty files
// and diffstat are all questions `git` will answer whether or not the agent
// thought to mention them. So a checkpoint gets two halves:
//
//	what the agent said    — task, decisions, dead ends, next step
//	what the repo was      — branch, sha, dirty files, diffstat
//
// Only the first can be forgotten. The second is why an arriving agent can trust
// "this was true at a3f9c2" rather than "this was true at some point".
//
// Everything here degrades to the zero value. A directory that is not a repo, a
// git that is not installed, a detached HEAD, a repo with no commits: each
// returns an empty State and no error, because a checkpoint that refuses to save
// because git was unavailable would be worse than one without a sha.
package gitstate

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// State is the repository as it stood when a checkpoint was written.
type State struct {
	// Branch is the current branch, or "" when detached or unavailable.
	Branch string
	// Commit is the short HEAD sha. The field that makes everything else
	// verifiable — a decision recorded against a commit can be checked out.
	Commit string
	// Subject is HEAD's commit subject, so the sha means something to a reader.
	Subject string
	// Dirty is the count of files with uncommitted changes. Zero means clean,
	// which is itself worth recording: "clean at this sha" is a much stronger
	// handoff than "somewhere near this sha".
	Dirty int
	// Files are the paths with uncommitted changes, capped. These are what the
	// session actually touched, observed rather than reported — an agent's own
	// Files list is a claim, and this is evidence.
	Files []string
	// Insertions and Deletions summarise the uncommitted diff.
	Insertions int
	Deletions  int
	// Worktree is set when this is a linked worktree rather than the main one,
	// because "same project, divergent parallel state" is exactly where
	// continuity breaks and the checkpoint should say which one it meant.
	Worktree string
}

// Empty reports whether anything was learned. Callers use it to decide whether
// to record a git section at all rather than writing an empty one.
func (s State) Empty() bool { return s.Commit == "" && s.Branch == "" }

// maxFiles bounds the recorded file list. A checkpoint is read by a model on a
// budget, and a hundred paths crowd out the decisions that matter more.
const maxFiles = 20

// gitTimeout bounds every call. A checkpoint must not hang because a repository
// is on a slow network mount or an index lock is held.
const gitTimeout = 3 * time.Second

// Read gathers the repository state at dir. Never returns an error: absence of
// git state is a normal condition, not a failure.
func Read(dir string) State {
	var s State
	if dir == "" || !isRepo(dir) {
		return s
	}

	// Branch first. A detached HEAD reports "HEAD", which is not a branch name
	// and would be misleading in a handoff, so it is dropped.
	if b := git(dir, "rev-parse", "--abbrev-ref", "HEAD"); b != "" && b != "HEAD" {
		s.Branch = b
	}
	s.Commit = git(dir, "rev-parse", "--short", "HEAD")
	if s.Commit != "" {
		s.Subject = git(dir, "log", "-1", "--pretty=%s")
	}

	// A linked worktree's git dir sits inside the main repo's common dir, so the
	// two differ. Naming it prevents the confusion where two agents check in
	// against "the same branch" from different trees.
	//
	// Both paths must be resolved before comparing: in the main tree
	// --git-common-dir answers with a *relative* ".git" while
	// --absolute-git-dir answers absolutely, so a naive comparison reports every
	// ordinary repository as a worktree.
	if gitDir, common := git(dir, "rev-parse", "--absolute-git-dir"), git(dir, "rev-parse", "--git-common-dir"); gitDir != "" && common != "" {
		if !filepath.IsAbs(common) {
			common = filepath.Join(dir, common)
		}
		if a, err1 := filepath.EvalSymlinks(gitDir); err1 == nil {
			if b, err2 := filepath.EvalSymlinks(common); err2 == nil && a != b {
				s.Worktree = git(dir, "rev-parse", "--show-toplevel")
			}
		}
	}

	s.Files, s.Dirty = dirtyFiles(dir)
	s.Insertions, s.Deletions = diffStat(dir)
	return s
}

func isRepo(dir string) bool {
	return git(dir, "rev-parse", "--is-inside-work-tree") == "true"
}

// dirtyFiles lists paths with uncommitted changes. The count is the true total
// even when the list is capped, because "3 of 47 files" and "3 files" are
// different situations and the second would be a lie.
func dirtyFiles(dir string) ([]string, int) {
	// gitLines, not git: porcelain output is column-significant, and the
	// leading status column is a space for an unstaged change (" M a.txt").
	// Trimming the whole output strips that space off the first line only, so
	// the fixed-offset parse below silently eats a character of the first
	// filename — "a.txt" became ".txt", on exactly one line of the output.
	out := gitLines(dir, "status", "--porcelain=v1", "--untracked-files=normal")
	if out == "" {
		return nil, 0
	}
	var files []string
	total := 0
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		// "XY path" — and for renames, "XY old -> new", where the new path is
		// the one that matters.
		path := strings.TrimSpace(line[3:])
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		path = strings.Trim(path, `"`)
		if path == "" {
			continue
		}
		total++
		if len(files) < maxFiles {
			files = append(files, path)
		}
	}
	return files, total
}

// diffStat totals the uncommitted change. Tracked files only — an untracked
// file has no diff to measure, and counting its whole length as insertions
// would overstate the change.
func diffStat(dir string) (insertions, deletions int) {
	out := git(dir, "diff", "--numstat", "HEAD")
	if out == "" {
		return 0, 0
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Binary files report "-", which is not a number and not a failure.
		if n, err := strconv.Atoi(fields[0]); err == nil {
			insertions += n
		}
		if n, err := strconv.Atoi(fields[1]); err == nil {
			deletions += n
		}
	}
	return insertions, deletions
}

// git runs one command and returns trimmed stdout, or "" on any failure.
//
// Errors are swallowed deliberately. Every caller here treats "could not find
// out" and "there is nothing to find out" identically, and a checkpoint is not
// the place to surface that git exited 128 because the repo has no commits yet.
func git(dir string, args ...string) string {
	return strings.TrimSpace(gitLines(dir, args...))
}

// gitLines is git without trimming leading whitespace, for output whose columns
// carry meaning. Only the trailing newline is removed.
func gitLines(dir string, args ...string) string {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	// A repository on a slow mount, or one whose index is locked by another
	// process, must not hold up a checkpoint.
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.Output()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(gitTimeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return ""
	}
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// Summary renders the state as one line for a checkpoint's frontmatter or a
// report. Empty when there is nothing to say.
func (s State) Summary() string {
	if s.Empty() {
		return ""
	}
	var parts []string
	if s.Branch != "" {
		parts = append(parts, s.Branch)
	}
	if s.Commit != "" {
		parts = append(parts, s.Commit)
	}
	switch {
	case s.Dirty == 0 && s.Commit != "":
		parts = append(parts, "clean")
	case s.Dirty == 1:
		parts = append(parts, "1 file uncommitted")
	case s.Dirty > 1:
		parts = append(parts, strconv.Itoa(s.Dirty)+" files uncommitted")
	}
	if s.Insertions > 0 || s.Deletions > 0 {
		parts = append(parts, "+"+strconv.Itoa(s.Insertions)+"/-"+strconv.Itoa(s.Deletions))
	}
	return strings.Join(parts, " · ")
}
