package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A submodule's common git dir is <super>/.git/modules/<name>. The import took
// its parent as the main checkout, looked under .git/modules, and said there
// was nothing to import. Claude Code files a submodule under its own path.
func TestAutoMemoryIsFoundFromASubmodule(t *testing.T) {
	standInRules(t, nil)
	projects := t.TempDir()
	t.Setenv("LOGOS_CLAUDE_PROJECTS", projects)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lib, super := filepath.Join(base, "lib"), filepath.Join(base, "super")
	for _, repo := range []string{lib, super} {
		git(t, base, "init", "-q", repo)
		git(t, repo, "commit", "-q", "--allow-empty", "-m", "first")
	}
	git(t, super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "lib")
	sub := filepath.Join(super, "lib")
	dir := encodedAutoMemoryDir(t, projects, sub)
	if err := os.WriteFile(filepath.Join(dir, "project_freeze.md"), []byte(projectNote), 0o644); err != nil {
		t.Fatal(err)
	}
	out := importRules(t, claudeAutoMemorySource, "--dir", sub)
	if n := len(pendingFrom(t, claudeAutoMemorySource)); n != 1 {
		t.Errorf("queued %d notes from a submodule, want its 1:\n%s", n, out)
	}
}

// A worktree of a bare repository /x/repo.git has /x as the common dir's
// parent, which is no checkout at all. Claude Code files such a worktree under
// the bare repository.
func TestAutoMemoryIsFoundFromAWorktreeOfABareRepository(t *testing.T) {
	standInRules(t, nil)
	projects := t.TempDir()
	t.Setenv("LOGOS_CLAUDE_PROJECTS", projects)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src, bare, tree := filepath.Join(base, "src"), filepath.Join(base, "repo.git"), filepath.Join(base, "tree")
	git(t, base, "init", "-q", src)
	git(t, src, "commit", "-q", "--allow-empty", "-m", "first")
	git(t, base, "clone", "-q", "--bare", src, bare)
	git(t, bare, "worktree", "add", "-q", tree)
	dir := encodedAutoMemoryDir(t, projects, bare)
	if err := os.WriteFile(filepath.Join(dir, "project_freeze.md"), []byte(projectNote), 0o644); err != nil {
		t.Fatal(err)
	}
	out := importRules(t, claudeAutoMemorySource, "--dir", tree)
	if n := len(pendingFrom(t, claudeAutoMemorySource)); n != 1 {
		t.Errorf("queued %d notes from a bare repository's worktree, want its 1:\n%s", n, out)
	}
}

// A worktree of a submodule has <super>/.git/modules/<name> as its common dir,
// not named .git, and Claude Code 2.1.316 files it under that git dir rather
// than under the submodule's checkout. The import reads the folder the host
// wrote, however odd its name.
func TestAutoMemoryIsFoundFromAWorktreeOfASubmoduleWhereClaudeCodeFilesIt(t *testing.T) {
	standInRules(t, nil)
	projects := t.TempDir()
	t.Setenv("LOGOS_CLAUDE_PROJECTS", projects)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lib, super, tree := filepath.Join(base, "lib"), filepath.Join(base, "super"), filepath.Join(base, "tree")
	for _, repo := range []string{lib, super} {
		git(t, base, "init", "-q", repo)
		git(t, repo, "commit", "-q", "--allow-empty", "-m", "first")
	}
	git(t, super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "lib")
	git(t, filepath.Join(super, "lib"), "worktree", "add", "-q", tree)
	dir := encodedAutoMemoryDir(t, projects, filepath.Join(super, ".git", "modules", "lib"))
	if err := os.WriteFile(filepath.Join(dir, "project_freeze.md"), []byte(projectNote), 0o644); err != nil {
		t.Fatal(err)
	}
	out := importRules(t, claudeAutoMemorySource, "--dir", tree)
	if n := len(pendingFrom(t, claudeAutoMemorySource)); n != 1 {
		t.Errorf("queued %d notes from a submodule's worktree, want its 1:\n%s", n, out)
	}
}
