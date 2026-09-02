package mcpserver

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/memory"
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
