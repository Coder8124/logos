package gitstate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The contract that matters most is not what this reads when git cooperates —
// it is that it never fails when git does not. A checkpoint that refuses to save
// because a directory was not a repository would trade a missing sha for lost
// work, which is the wrong way round.

func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func commit(t *testing.T, dir, name, body, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", message}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func TestReadsBranchAndCommit(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\n", "the first commit")

	s := Read(dir)
	if s.Empty() {
		t.Fatal("a repository with a commit reported no state")
	}
	if s.Branch != "main" {
		t.Errorf("branch = %q, want main", s.Branch)
	}
	if len(s.Commit) < 6 {
		t.Errorf("commit = %q, want a short sha", s.Commit)
	}
	if s.Subject != "the first commit" {
		t.Errorf("subject = %q", s.Subject)
	}
	if s.Dirty != 0 {
		t.Errorf("dirty = %d on a clean tree, want 0", s.Dirty)
	}
	// "clean at this sha" is a much stronger handoff than "near this sha", so
	// the summary has to say it.
	if !strings.Contains(s.Summary(), "clean") {
		t.Errorf("summary %q does not report a clean tree", s.Summary())
	}
}

// The evidence half: what the session actually touched, observed rather than
// reported. An agent's own Files list is a claim; this is not.
func TestReadsUncommittedWork(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\ntwo\nthree\n", "base")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nCHANGED\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := Read(dir)
	if s.Dirty != 2 {
		t.Errorf("dirty = %d, want 2 (one modified, one untracked)", s.Dirty)
	}
	found := map[string]bool{}
	for _, f := range s.Files {
		found[f] = true
	}
	for _, want := range []string{"a.txt", "b.txt"} {
		if !found[want] {
			t.Errorf("%s missing from %v", want, s.Files)
		}
	}
	// Tracked-file diff only: counting an untracked file's whole length as
	// insertions would overstate the change.
	if s.Insertions != 1 || s.Deletions != 1 {
		t.Errorf("diffstat = +%d/-%d, want +1/-1", s.Insertions, s.Deletions)
	}
}

// Every one of these is a normal condition, not a failure, and each must
// produce an empty State rather than a panic or an error.
func TestDegradesEverywhere(t *testing.T) {
	for _, name := range []string{"not a repo", "missing directory", "empty path"} {
		var dir string
		switch name {
		case "not a repo":
			dir = t.TempDir()
		case "missing directory":
			dir = filepath.Join(t.TempDir(), "nope")
		case "empty path":
			dir = ""
		}
		s := Read(dir)
		if !s.Empty() {
			t.Errorf("%s: reported state %+v, want empty", name, s)
		}
		if s.Summary() != "" {
			t.Errorf("%s: summary = %q, want empty", name, s.Summary())
		}
	}
}

// A fresh repository with no commits at all: git exits non-zero for HEAD, which
// must read as "nothing to say" rather than breaking the checkpoint.
func TestRepoWithNoCommits(t *testing.T) {
	dir := repo(t)
	s := Read(dir)
	if s.Commit != "" {
		t.Errorf("commit = %q in a repo with no commits", s.Commit)
	}
	// The branch exists even before the first commit, so state is not required
	// to be empty — only sane.
	if s.Dirty != 0 {
		t.Errorf("dirty = %d in an empty repo", s.Dirty)
	}
}

// A detached HEAD reports "HEAD" from rev-parse, which is not a branch name and
// would be misleading in a handoff.
func TestDetachedHeadReportsNoBranch(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\n", "first")
	commit(t, dir, "a.txt", "two\n", "second")

	sha := strings.TrimSpace(git(dir, "rev-parse", "HEAD"))
	if out, err := exec.Command("git", "-C", dir, "checkout", "--detach", sha).CombinedOutput(); err != nil {
		t.Skipf("could not detach: %v: %s", err, out)
	}

	s := Read(dir)
	if s.Branch != "" {
		t.Errorf("branch = %q on a detached HEAD, want empty", s.Branch)
	}
	if s.Commit == "" {
		t.Error("a detached HEAD still has a commit, which is the useful part")
	}
}

// Worktrees are where continuity breaks hardest — same project, divergent
// parallel state — so a checkpoint has to be able to say which tree it meant.
func TestLinkedWorktreeIsNamed(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\n", "first")

	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", "-b", "side", wt).CombinedOutput(); err != nil {
		t.Skipf("worktree unavailable: %v: %s", err, out)
	}

	main := Read(dir)
	if main.Worktree != "" {
		t.Errorf("the main tree was named as a worktree: %q", main.Worktree)
	}

	side := Read(wt)
	if side.Worktree == "" {
		t.Error("a linked worktree was not identified as one")
	}
	if side.Branch != "side" {
		t.Errorf("worktree branch = %q, want side", side.Branch)
	}
}

// The name is what continuity is scoped by, so it has to be git's own — stable
// across a move, and unique inside the repository.
func TestWorktreeNameIsGitsOwnName(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\n", "first")

	// Two worktrees whose folders share a basename. A scope taken from the
	// folder would call both "same" and merge them, which is the exact
	// collision worktree scoping exists to prevent; git allocates the second a
	// name of its own.
	parents := []string{filepath.Join(t.TempDir(), "left"), filepath.Join(t.TempDir(), "right")}
	names := make([]string, 0, 2)
	for i, parent := range parents {
		wt := filepath.Join(parent, "same")
		branch := "side" + strconv.Itoa(i)
		if out, err := exec.Command("git", "-C", dir, "worktree", "add", "-b", branch, wt).CombinedOutput(); err != nil {
			t.Skipf("worktree unavailable: %v: %s", err, out)
		}
		n := WorktreeName(wt)
		if n == "" {
			t.Fatalf("%s was not identified as a worktree", wt)
		}
		names = append(names, n)
	}
	if names[0] == names[1] {
		t.Errorf("two worktrees share the name %q, so their continuity would merge", names[0])
	}

	// The main checkout is not a worktree, which is what keeps a repository
	// with no linked worktrees behaving exactly as it did.
	if n := WorktreeName(dir); n != "" {
		t.Errorf("the main checkout was named as a worktree: %q", n)
	}
}

// Same rule as everything else here: not knowing is a normal condition.
func TestWorktreeNameDegradesToNothing(t *testing.T) {
	for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "nope"), ""} {
		if n := WorktreeName(dir); n != "" {
			t.Errorf("WorktreeName(%q) = %q, want empty", dir, n)
		}
	}
}

// #197: git status and git diff consult core.fsmonitor, and a repository
// unpacked from an archive carries its own .git/config. Reading the state of a
// folder the user merely opened must not run a program that folder names.
func TestReadingARepositoryDoesNotRunItsFsmonitor(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\n", "the first commit")
	marker := filepath.Join(t.TempDir(), "ran")
	hook := filepath.Join(t.TempDir(), "monitor.sh")
	script := "#!/bin/sh\ntouch " + strconv.Quote(marker) + "\n"
	if err := os.WriteFile(hook, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", dir, "config", "core.fsmonitor", hook)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if s := Read(dir); s.Empty() {
		t.Fatal("the repository reported no state")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("reading the repository ran the command its core.fsmonitor names")
	}
}

// trap writes a script that leaves a marker when run and passes stdin through,
// so a filter that runs it still hands git back the file.
func trap(t *testing.T) (script, marker string) {
	t.Helper()
	marker = filepath.Join(t.TempDir(), "ran")
	script = filepath.Join(t.TempDir(), "trap.sh")
	body := "#!/bin/sh\ntouch " + strconv.Quote(marker) + "\ncat\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return script, marker
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "protocol.file.allow=always"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func assertNotRun(t *testing.T, marker, what string) {
	t.Helper()
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("reading the repository ran the command %s names", what)
	}
}

// #197, found reviewing the fsmonitor fix: status and diff also pass every
// changed file through the clean filter its attributes name, and the filter's
// command comes from the same .git/config. Declared required, too, so a fix
// that only blanks the command makes git refuse the read instead.
func TestReadingARepositoryDoesNotRunItsCleanFilter(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\n", "the first commit")
	script, marker := trap(t)
	gitRun(t, dir, "config", "filter.ev.clean", script)
	gitRun(t, dir, "config", "filter.ev.required", "true")
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "attributes"), []byte("*.txt filter=ev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Read(dir)
	assertNotRun(t, marker, "filter.ev.clean")
	if s.Dirty != 1 {
		t.Fatalf("Dirty = %d, want the changed file still counted", s.Dirty)
	}
}

// #197: with log.showSignature set, reading HEAD's subject verifies its
// signature by running gpg.program — and the signature only has to be present,
// not valid, so a hand-made commit object is enough.
func TestReadingARepositoryDoesNotRunItsSignatureProgram(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\n", "the first commit")
	body := "tree " + gitRun(t, dir, "rev-parse", "HEAD^{tree}") + "\n" +
		"parent " + gitRun(t, dir, "rev-parse", "HEAD") + "\n" +
		"author t <t@t> 1 +0000\ncommitter t <t@t> 1 +0000\n" +
		"gpgsig -----BEGIN PGP SIGNATURE-----\n \n -----END PGP SIGNATURE-----\n\nsigned\n"
	obj := filepath.Join(t.TempDir(), "commit")
	if err := os.WriteFile(obj, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "update-ref", "refs/heads/main", gitRun(t, dir, "hash-object", "-t", "commit", "-w", obj))
	script, marker := trap(t)
	gitRun(t, dir, "config", "log.showSignature", "true")
	gitRun(t, dir, "config", "gpg.program", script)
	s := Read(dir)
	assertNotRun(t, marker, "gpg.program")
	if s.Subject != "signed" {
		t.Fatalf("Subject = %q, want the signed commit's", s.Subject)
	}
}

// #197: status and diff recurse into submodules, and a submodule's config
// lives under .git/modules — where the overrides for the outer repository's
// filters do not reach.
func TestReadingARepositoryDoesNotRunASubmodulesFilter(t *testing.T) {
	sub := repo(t)
	commit(t, sub, "s.txt", "one\n", "the submodule")
	dir := repo(t)
	commit(t, dir, "a.txt", "one\n", "the first commit")
	gitRun(t, dir, "submodule", "add", sub, "sm")
	gitRun(t, dir, "commit", "-m", "add the submodule")
	script, marker := trap(t)
	gitRun(t, filepath.Join(dir, "sm"), "config", "filter.sv.clean", script)
	if err := os.MkdirAll(filepath.Join(dir, ".git", "modules", "sm", "info"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "modules", "sm", "info", "attributes"), []byte("*.txt filter=sv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sm", "s.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	Read(dir)
	assertNotRun(t, marker, "the submodule's filter.sv.clean")
}

// #197, second review: status and diff refresh the index and write it back
// when a file's stat data is stale — which every file's is after an archive is
// unpacked — and writing the index runs the post-index-change hook the
// repository ships in .git/hooks.
func TestReadingARepositoryDoesNotRunItsIndexHook(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\n", "the first commit")
	script, marker := trap(t)
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "post-index-change"), body, 0o700); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "a.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	Read(dir)
	assertNotRun(t, marker, "post-index-change")
}

// The user's own filters are not the threat: git-lfs installs filter.lfs in
// the global config, and blanking it makes every LFS file with stale stat data
// read as modified — the whole binary counted as an uncommitted change.
func TestAFilterFromTheUsersOwnConfigStillRuns(t *testing.T) {
	global := filepath.Join(t.TempDir(), "gitconfig")
	cfg := "[filter \"up\"]\n\tclean = tr a-z A-Z\n\tsmudge = cat\n"
	if err := os.WriteFile(global, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	dir := repo(t)
	commit(t, dir, ".gitattributes", "*.bin filter=up\n", "attributes")
	commit(t, dir, "f.bin", "abc\n", "a filtered file")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "f.bin"), later, later); err != nil {
		t.Fatal(err)
	}
	if s := Read(dir); s.Dirty != 0 {
		t.Fatalf("Dirty = %d (%v), want the untouched filtered file clean", s.Dirty, s.Files)
	}
}

// #197, third review: git 2.54 also takes hooks from config (hook.<name>.event
// and .command), which core.hooksPath does not reach — and diff writes a stale
// index back even with optional locks off, so post-index-change still fired.
func TestReadingARepositoryDoesNotRunAHookItsConfigDefines(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\n", "the first commit")
	script, marker := trap(t)
	gitRun(t, dir, "config", "hook.evil.event", "post-index-change")
	gitRun(t, dir, "config", "hook.evil.command", script)
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "a.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	Read(dir)
	assertNotRun(t, marker, "hook.evil.command")
}

// A hook the repository's config defines is switched off, not only kept from
// the index write: git adds events, and the next one may fire on a read.
func TestAHookTheRepositoryDefinesIsSwitchedOff(t *testing.T) {
	dir := repo(t)
	gitRun(t, dir, "config", "hook.evil.event", "post-index-change")
	gitRun(t, dir, "config", "hook.evil.command", "/bin/true")
	args, err := safeArgs(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "hook.evil.enabled=false") {
		t.Errorf("hook.evil left live: %v", args)
	}
}

// git-lfs runs lfs.extension.<name>.clean from inside the user's own filter,
// so an extension the repository brings turns that filter off here — and only
// here: a repository without one keeps git-lfs working.
func TestAnLFSExtensionTheRepositoryBringsTurnsTheLFSFilterOff(t *testing.T) {
	dir := repo(t)
	args, err := safeArgs(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "filter.lfs") {
		t.Fatalf("filter.lfs overridden in a repository with no extension: %v", args)
	}
	gitRun(t, dir, "config", "lfs.extension.evil.clean", "/bin/true")
	args, err = safeArgs(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "filter.lfs.process=") {
		t.Errorf("filter.lfs left live beside lfs.extension.evil: %v", args)
	}
}

// diff-index, unlike diff, does not look for renames unless asked, and a
// staged move of an unchanged file then counted as the whole file deleted and
// added again — a refactor that moved files read as a rewrite.
func TestAMovedFileIsNotCountedAsRewritten(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "big.txt", strings.Repeat("a line\n", 100), "the first commit")
	gitRun(t, dir, "mv", "big.txt", "moved.txt")
	s := Read(dir)
	if s.Insertions != 0 || s.Deletions != 0 {
		t.Errorf("diffstat = +%d/-%d for a pure move, want +0/-0", s.Insertions, s.Deletions)
	}
}

// Config section and key names are case-insensitive, so the repository can
// spell the section [lfs "Extension.evil"], and git reports it with that case.
func TestAnLFSExtensionSpelledInAnotherCaseStillTurnsTheLFSFilterOff(t *testing.T) {
	dir := repo(t)
	gitRun(t, dir, "config", "lfs.Extension.evil.clean", "/bin/true")
	args, err := safeArgs(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "filter.lfs.process=") {
		t.Errorf("filter.lfs left live beside lfs.Extension.evil: %v", args)
	}
}

// Rename detection that compares every deleted file with every added one ran
// past the timeout on a large restructuring, and a timed-out read reads as no
// change at all: the biggest change a session made was recorded as a clean
// tree with +0/-0. The timeout is lowered so the repository can be small —
// unbounded, this one takes about twice the lowered timeout; bounded, a tenth.
func TestALargeRestructuringIsStillCounted(t *testing.T) {
	defer func(d time.Duration) { gitTimeout = d }(gitTimeout)
	gitTimeout = 750 * time.Millisecond
	dir := repo(t)
	// Every line unique to its file, so no pair is similar and rename
	// detection has to score all of them.
	write := func(kind string) {
		for i := 0; i < 900; i++ {
			var body strings.Builder
			for j := 0; j < 600; j++ {
				fmt.Fprintf(&body, "%s %d line %d\n", kind, i, j)
			}
			name := filepath.Join(dir, fmt.Sprintf("%s%d.txt", kind, i))
			if err := os.WriteFile(name, []byte(body.String()), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("old")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "the old layout")
	gitRun(t, dir, "rm", "-q", "old*.txt")
	write("new")
	gitRun(t, dir, "add", ".")
	s := Read(dir)
	if s.Insertions == 0 && s.Deletions == 0 {
		t.Error("a 900-file restructuring was recorded as +0/-0")
	}
	// status finds renames too, at the same cost, and a timed-out status
	// reads as a clean tree.
	if s.Dirty == 0 {
		t.Error("a 900-file restructuring was recorded as a clean tree")
	}
}

// Exact moves are not the only renames worth finding: two files renamed with a
// line added to each are a two-line change, not two files rewritten.
func TestRenamingAFewFilesWithSmallEditsCountsTheEditsOnly(t *testing.T) {
	dir := repo(t)
	var body strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&body, "line %d\n", i)
	}
	commit(t, dir, "a.go", "a\n"+body.String(), "the first commit")
	commit(t, dir, "b.go", "b\n"+body.String(), "the second commit")
	gitRun(t, dir, "mv", "a.go", "a_v2.go")
	gitRun(t, dir, "mv", "b.go", "b_v2.go")
	for _, f := range []string{"a_v2.go", "b_v2.go"} {
		p := filepath.Join(dir, f)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append(b, "added\n"...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, dir, "add", ".")
	if s := Read(dir); s.Insertions != 2 || s.Deletions != 0 {
		t.Errorf("diffstat = +%d/-%d, want +2/-0", s.Insertions, s.Deletions)
	}
}

// A partial clone fetches a missing blob on demand, and diff-index needs the
// old contents of every changed file. The fetch runs the upload-pack command
// the repository's own config names — and is a network call besides.
func TestReadingAPartialCloneFetchesNothing(t *testing.T) {
	server := repo(t)
	commit(t, server, "a.txt", "one\n", "the first commit")
	gitRun(t, server, "config", "uploadpack.allowFilter", "true")
	dir := filepath.Join(t.TempDir(), "clone")
	gitRun(t, server, "clone", "-q", "--no-checkout", "--filter=blob:none", "file://"+server, dir)
	// Populate the index from HEAD without checking out, so a.txt's blob
	// stays unfetched, then change the file in the working tree.
	gitRun(t, dir, "reset", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ran")
	pack := filepath.Join(t.TempDir(), "pack.sh")
	body := "#!/bin/sh\ntouch " + strconv.Quote(marker) + "\nexec git-upload-pack \"$@\"\n"
	if err := os.WriteFile(pack, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "config", "remote.origin.uploadpack", pack)
	Read(dir)
	assertNotRun(t, marker, "remote.origin.uploadpack")

	// A git older than 2.45 ignores GIT_NO_LAZY_FETCH and has only the
	// protocol overrides, which a repository can loosen per transport:
	// protocol.<name>.allow is read before protocol.allow.
	// A user's GIT_ALLOW_PROTOCOL, a common hardening, is checked before any
	// of those settings, so the one it names is let through whatever -c says.
	gitRun(t, dir, "config", "protocol.file.allow", "always")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	args, err := safeArgs(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	old := exec.Command("git", append(append(args, "-C", dir), "diff-index", "--numstat", "HEAD")...)
	// Dropped from the user's environment as well as safeEnv's, or a machine
	// that exports it would pass without ever taking the old-git path.
	for _, kv := range append(os.Environ(), safeEnv...) {
		if !strings.HasPrefix(kv, "GIT_NO_LAZY_FETCH=") {
			old.Env = append(old.Env, kv)
		}
	}
	_ = old.Run()
	assertNotRun(t, marker, "remote.origin.uploadpack, without GIT_NO_LAZY_FETCH")
}
