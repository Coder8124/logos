package mcpserver

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/session"
)

// testDB is an open, initialised store on a scratch file. Single connection,
// matching how the served process opens it.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := memory.Init(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// The point of scoping is that two folders do not pollute each other without
// anyone noticing. These test the resolution rules directly, because the
// failure they guard against is silent: a wrong project still stores and still
// recalls, it just mixes two pieces of work.

func TestProjectFromPathUsesTheFolderName(t *testing.T) {
	if got := projectFromPath("/Users/x/code/kestrel"); got != "kestrel" {
		t.Fatalf("want kestrel, got %q", got)
	}
	// The same work at a different path is the same project, not a new one.
	if got := projectFromPath("/elsewhere/kestrel/"); got != "kestrel" {
		t.Fatalf("want kestrel, got %q", got)
	}
}

func TestProjectFromPathRefusesRootAndHome(t *testing.T) {
	if got := projectFromPath("/"); got != "" {
		t.Fatalf("filesystem root should not name a project, got %q", got)
	}
	if got := projectFromPath(""); got != "" {
		t.Fatalf("empty path should not name a project, got %q", got)
	}
	// A host launched in the home directory is a host with nothing open.
	// Scoping that to a project named after the user would bucket every
	// unrelated session together, which is worse than staying global.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := projectFromPath(home); got != "" {
		t.Fatalf("home directory should not name a project, got %q", got)
	}
}

func TestRootsBeatCwdAndURIsAreDecoded(t *testing.T) {
	roots := []string{"file:///Users/x/code/my%20project"}
	if got := projectFromRoots(roots); got != "my project" {
		t.Fatalf("want %q, got %q", "my project", got)
	}
	// A bare path is accepted too: hosts send both, and dropping the scope
	// over a URI that will not parse is the wrong trade.
	if got := projectFromRoots([]string{"/Users/x/code/kestrel"}); got != "kestrel" {
		t.Fatalf("want kestrel, got %q", got)
	}
}

func TestRootsFromInitializeReadsBothShapes(t *testing.T) {
	got := rootsFromInitialize(json.RawMessage(`{"rootUri":"file:///a/one"}`))
	if len(got) != 1 || got[0] != "file:///a/one" {
		t.Fatalf("rootUri not read: %v", got)
	}
	got = rootsFromInitialize(json.RawMessage(`{"roots":[{"uri":"file:///a/two"},{"path":"/a/three"}]}`))
	if len(got) != 2 || got[0] != "file:///a/two" || got[1] != "/a/three" {
		t.Fatalf("roots array not read: %v", got)
	}
	// Malformed params must not panic or poison the scope; cwd still answers.
	if got := rootsFromInitialize(json.RawMessage(`not json`)); got != nil {
		t.Fatalf("want nil for unparseable params, got %v", got)
	}
}

func TestExplicitArgumentOutranksEverything(t *testing.T) {
	s := &Server{roots: []string{"/a/from-roots"}}
	t.Setenv("BRAIN_PROJECT", "from-env")
	if got := s.resolveProject("explicit"); got != "explicit" {
		t.Fatalf("explicit argument must win, got %q", got)
	}
}

func TestEnvOutranksRootsAndCwd(t *testing.T) {
	s := &Server{roots: []string{"/a/from-roots"}}
	t.Setenv("BRAIN_PROJECT", "from-env")
	if got := s.resolveProject(""); got != "from-env" {
		t.Fatalf("BRAIN_PROJECT must win over roots, got %q", got)
	}
}

func TestRootsOutrankCwd(t *testing.T) {
	s := &Server{roots: []string{"file:///a/from-roots"}}
	t.Setenv("BRAIN_PROJECT", "")
	if got := s.resolveProject(""); got != "from-roots" {
		t.Fatalf("roots must win over cwd, got %q", got)
	}
}

func TestSessionProjectIsResolvedOnce(t *testing.T) {
	// A stray chdir mid-session must not silently re-scope the rest of it.
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{roots: []string{real}}
	t.Setenv("BRAIN_PROJECT", "")
	first := s.resolveProject("")
	s.roots = []string{"/somewhere/else"}
	if second := s.resolveProject(""); second != first {
		t.Fatalf("project changed mid-session: %q then %q", first, second)
	}
}

func TestFallsBackToGlobalWhenNothingIdentifiesAProject(t *testing.T) {
	s := &Server{roots: []string{"/"}}
	t.Setenv("BRAIN_PROJECT", "")
	t.Chdir("/")
	if got := s.resolveProject(""); got != "" {
		t.Fatalf("want global (empty), got %q", got)
	}
}

func TestArgBoolAcceptsStringsModelsActuallyEmit(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want bool
	}{
		{true, true},
		{"true", true},
		{"false", false},
		{" true ", true},
		{nil, false},
		{"nonsense", false},
	} {
		if got := argBool(map[string]any{"k": tc.in}, "k", false); got != tc.want {
			t.Errorf("argBool(%#v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// The end-to-end claim: what one folder remembers, another folder does not see.
func TestTwoProjectsDoNotSeeEachOther(t *testing.T) {
	db := testDB(t)
	s := &Server{DB: db, vault: t.TempDir()}
	t.Setenv("BRAIN_PROJECT", "alpha")
	if _, err := s.remember("the alpha frame is aluminium", "fact", "", false); err != nil {
		t.Fatal(err)
	}

	// A second session, in a different folder.
	other := &Server{DB: db, vault: s.vault}
	t.Setenv("BRAIN_PROJECT", "beta")
	out, err := other.recall("frame material", 10, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "aluminium") {
		t.Fatalf("beta saw alpha's memory:\n%s", out)
	}

	// …unless it asks, which is the escape hatch the tool description names.
	out, err = other.recall("frame material", 10, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "aluminium") {
		t.Fatalf("all_projects should have found it:\n%s", out)
	}
}

func TestGlobalMemoriesReachEveryProject(t *testing.T) {
	db := testDB(t)
	s := &Server{DB: db, vault: t.TempDir()}
	t.Setenv("BRAIN_PROJECT", "alpha")
	if _, err := s.remember("the user prefers short replies", "preference", "", true); err != nil {
		t.Fatal(err)
	}

	other := &Server{DB: db, vault: s.vault}
	t.Setenv("BRAIN_PROJECT", "beta")
	out, err := other.recall("how should I reply", 10, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "short replies") {
		t.Fatalf("a global memory should reach every project:\n%s", out)
	}
}

// A git worktree is its own continuity context.
//
// The failure guarded against is the same silent one as project scoping, one
// level down: two worktrees of a repository are two people working on two
// things, and an agent that resumes into the other tree's checkpoint is not
// merely untidy — it continues work it never started.

// detectWorktrees clears any BRAIN_WORKTREE the developer happens to have set,
// so these tests exercise detection rather than the override.
func detectWorktrees(t *testing.T) {
	t.Helper()
	if old, ok := os.LookupEnv("BRAIN_WORKTREE"); ok {
		t.Cleanup(func() { os.Setenv("BRAIN_WORKTREE", old) })
		os.Unsetenv("BRAIN_WORKTREE")
	}
}

// gitRepo is a repository with one commit. Skips rather than fails when git is
// unavailable: this is a scoping test, not a git test.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	detectWorktrees(t)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
		{"commit", "--allow-empty", "-m", "first"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

// linkedTree adds a worktree of repo on a branch of its own and returns it.
func linkedTree(t *testing.T, repo, name string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), name)
	add := []string{"-C", repo, "worktree", "add", "-b", name, wt}
	if out, err := exec.Command("git", add...).CombinedOutput(); err != nil {
		t.Skipf("worktrees unavailable: %v: %s", err, out)
	}
	return wt
}

// The requirement this change lives or dies by: a repository with no linked
// worktrees is scoped exactly as it was, by its folder and nothing else.
func TestAMainCheckoutIsScopedAsBefore(t *testing.T) {
	dir := gitRepo(t)
	s := &Server{roots: []string{dir}}
	t.Setenv("BRAIN_PROJECT", "")
	if got, want := s.resolveScope(""), filepath.Base(dir); got != want {
		t.Fatalf("scope = %q, want %q — the main checkout must not move", got, want)
	}
}

// And a directory that is not a repository at all still gets a scope, because
// brain is not only used on git projects.
func TestANonGitDirectoryIsStillScoped(t *testing.T) {
	detectWorktrees(t)
	dir := t.TempDir()
	s := &Server{roots: []string{dir}}
	t.Setenv("BRAIN_PROJECT", "")
	if got, want := s.resolveScope(""), filepath.Base(dir); got != want {
		t.Fatalf("scope = %q, want %q", got, want)
	}
}

func TestTwoWorktreesOfOneRepoGetDistinctContinuity(t *testing.T) {
	repo := gitRepo(t)
	a, b := linkedTree(t, repo, "feature-a"), linkedTree(t, repo, "feature-b")

	// Both told they are the same project. This is the case folder names cannot
	// save you from: one BRAIN_PROJECT, two trees.
	t.Setenv("BRAIN_PROJECT", "kestrel")
	sa, sb := &Server{roots: []string{a}}, &Server{roots: []string{b}}
	got, other := sa.resolveScope(""), sb.resolveScope("")
	if got == other {
		t.Fatalf("both worktrees resolved to %q, so they share one continuity", got)
	}
	// Sub-scopes, not new projects: each stays inside kestrel's.
	for _, scope := range []string{got, other} {
		if !strings.HasPrefix(scope, "kestrel/") {
			t.Errorf("scope %q left the project it belongs to", scope)
		}
	}
}

// The worktree narrows an explicitly-given project too. Every continuity tool
// requires a project argument, so a rule that let one through unnarrowed would
// leave worktree scoping switched off in precisely the case it exists for —
// and no model can be expected to know which of two identical trees it is in.
func TestAnExplicitProjectIsStillNarrowedByTheWorktree(t *testing.T) {
	wt := linkedTree(t, gitRepo(t), "feature-a")
	s := &Server{roots: []string{wt}}
	t.Setenv("BRAIN_PROJECT", "")
	if got := s.resolveScope("kestrel"); got != "kestrel/feature-a" {
		t.Fatalf("scope = %q, want kestrel/feature-a", got)
	}
	// …unless the caller qualified the scope itself, which is how one tree
	// addresses another's continuity, or a file path reaches context untouched.
	other := &Server{roots: []string{wt}}
	if got := other.resolveScope("kestrel/feature-b"); got != "kestrel/feature-b" {
		t.Fatalf("scope = %q, want the scope as given", got)
	}
}

// Two trees a user deliberately wants to treat as one piece of work have no
// other way to say so, because nothing here takes the model's word for which
// tree it is in.
func TestBrainWorktreeTurnsTheNarrowingOff(t *testing.T) {
	repo := gitRepo(t)
	a, b := linkedTree(t, repo, "feature-a"), linkedTree(t, repo, "feature-b")
	t.Setenv("BRAIN_PROJECT", "kestrel")
	t.Setenv("BRAIN_WORKTREE", "")
	sa, sb := &Server{roots: []string{a}}, &Server{roots: []string{b}}
	if got, other := sa.resolveScope(""), sb.resolveScope(""); got != "kestrel" || other != "kestrel" {
		t.Fatalf("scopes = %q and %q, want both kestrel", got, other)
	}
}

// The end-to-end claim, one level below TestTwoProjectsDoNotSeeEachOther: what
// one worktree checkpoints, the other does not resume into — while both keep
// the memory of the repository they are both working on.
func TestOneWorktreeDoesNotResumeIntoAnother(t *testing.T) {
	db := testDB(t)
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	vault := t.TempDir()
	repo := gitRepo(t)
	t.Setenv("BRAIN_PROJECT", "kestrel")

	a := &Server{DB: db, vault: vault, roots: []string{linkedTree(t, repo, "feature-a")}}
	b := &Server{DB: db, vault: vault, roots: []string{linkedTree(t, repo, "feature-b")}}

	if _, err := a.remember("the frame is aluminium", "fact", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.checkpoint(map[string]any{
		"agent": "claude", "task": "re-quote the waveguide",
		"failed": []any{"quoting at volume — no movement"},
		"next":   "get a firm quote on the single-mic line",
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.checkpoint(map[string]any{
		"agent": "cursor", "task": "port the CLI to the new router",
		"next": "delete the shim",
	}, ""); err != nil {
		t.Fatal(err)
	}

	// Each tree's checkpoint is a file inside the project's folder, not beside
	// it: a worktree is a sub-scope of kestrel, not a second kestrel.
	for _, name := range []string{"feature-a", "feature-b"} {
		found, _ := filepath.Glob(filepath.Join(vault, "sessions", "kestrel", name, "*.md"))
		if len(found) != 1 {
			t.Fatalf("want one checkpoint under sessions/kestrel/%s, got %v", name, found)
		}
	}

	out, err := b.resume("", "cursor", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "waveguide") {
		t.Errorf("feature-b resumed into feature-a's work:\n%s", out)
	}
	if !strings.Contains(out, "new router") {
		t.Errorf("feature-b did not get its own checkpoint back:\n%s", out)
	}
	// The repository's memory is shared, because a worktree is the same
	// repository — losing that would make it a different project instead.
	mem, err := b.recall("frame material", 10, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mem, "aluminium") {
		t.Errorf("a worktree lost the repository's memory:\n%s", mem)
	}
}

// A worktree created this morning has no checkpoint of its own. The project's
// is still the best account of where the repository stands, so it is offered —
// labelled, because reading it as this tree's own stopping place is the mistake
// the label exists to prevent.
func TestAFreshWorktreeInheritsTheProjectsCheckpointAndSaysSo(t *testing.T) {
	db := testDB(t)
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	vault := t.TempDir()
	repo := gitRepo(t)
	t.Setenv("BRAIN_PROJECT", "kestrel")

	main := &Server{DB: db, vault: vault, roots: []string{repo}}
	if _, err := main.checkpoint(map[string]any{
		"agent": "claude", "task": "re-quote the waveguide", "next": "a firm quote",
	}, ""); err != nil {
		t.Fatal(err)
	}

	fresh := &Server{DB: db, vault: vault, roots: []string{linkedTree(t, repo, "feature-a")}}
	out, err := fresh.resume("", "cursor", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "waveguide") {
		t.Fatalf("a fresh worktree should still see where the repository stands:\n%s", out)
	}
	if !strings.Contains(out, "not this worktree's") {
		t.Errorf("the inherited checkpoint was not labelled as the project's:\n%s", out)
	}
}
