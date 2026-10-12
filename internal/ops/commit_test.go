package ops

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/procedure"
	_ "modernc.org/sqlite"
)

func vaultDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := memory.Init(db); err != nil {
		t.Fatal(err)
	}
	memory.SetVault(db, t.TempDir())
	t.Cleanup(func() { memory.SetVault(db, ""); db.Close() })
	return db
}

func gitRepo(t *testing.T) string {
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
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func commitFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "change " + name}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// A decision's reason is about the code as it was. Once the file it names has
// been changed, recall still returns it — the decision may well hold — but
// says the ground moved, in the words a ruling uses.
func TestADecisionWhoseNamedFileChangedIsFlaggedAtRecall(t *testing.T) {
	db, dir := vaultDB(t), gitRepo(t)
	commitFile(t, dir, "gen.go", "v1\n")
	if _, err := Remember(db, RememberRequest{Dir: dir, Memory: memory.Memory{
		Text: "Decided to keep gen.go checked in, because go generate needs protoc", Kind: memory.Fact, Source: "manual",
	}}); err != nil {
		t.Fatal(err)
	}
	q := RecallQuery{Query: "gen.go checked in", Limit: 5, Dir: dir}
	r, err := Recall(db, q)
	if err != nil || len(r.Memories) != 1 || r.Memories[0].Commit == "" || strings.Contains(r.Text(""), "⚠") {
		t.Fatalf("want one memory recorded at HEAD and not flagged, got %v\n%+v", err, r.Memories)
	}

	commitFile(t, dir, "gen.go", "v2\n")
	if r, err = Recall(db, q); err != nil {
		t.Fatal(err)
	}
	if len(r.Memories) != 1 || r.Memories[0].Drift == nil {
		t.Fatalf("recall returned %+v, want the decision marked as drifted", r.Memories)
	}
	if got := r.Text(""); !strings.Contains(got, "⚠") || !strings.Contains(got, "gen.go changed in 1 commit since") || !strings.Contains(got, "may no longer hold") {
		t.Errorf("recall said:\n%s\nwant the drift note", got)
	}
}

// A procedure is never in ordinary recall; before_you_try is where it is read,
// and where a trap about a rewritten file would send an agent the wrong way.
func TestAProcedureWhoseNamedFileChangedIsFlaggedAtRecall(t *testing.T) {
	db, dir := vaultDB(t), gitRepo(t)
	commitFile(t, dir, "gen.go", "v1\n")
	if _, err := Remember(db, RememberRequest{Dir: dir, Memory: memory.Memory{
		Text: "route: run go generate before build | trap: gen.go goes stale and the build passes on old output",
		Kind: memory.Procedure, Source: "manual",
	}}); err != nil {
		t.Fatal(err)
	}
	check := func() string {
		t.Helper()
		corpus, err := memory.RecallProcedures(db, nil, "", "", 0)
		if err != nil {
			t.Fatal(err)
		}
		hits, err := procedure.Check(corpus, nil, "", "run go generate before build", "", 4)
		if err != nil || len(hits) != 1 {
			t.Fatalf("want the procedure found, got %v %+v", err, hits)
		}
		procedure.MarkDrift(hits, dir)
		return procedure.Render(hits)
	}
	if out := check(); strings.Contains(out, "may no longer hold") {
		t.Fatalf("an untouched file was flagged:\n%s", out)
	}
	commitFile(t, dir, "gen.go", "v2\n")
	if out := check(); !strings.Contains(out, "⚠ recorded at") || !strings.Contains(out, "gen.go changed in 1 commit since, so it may no longer hold") {
		t.Errorf("before_you_try said:\n%s\nwant the drift note", out)
	}
}

// Every memory written before this existed has no commit. It cannot be
// measured, and saying nothing is truer than guessing it moved.
func TestAMemoryWithNoCommitIsNeverFlagged(t *testing.T) {
	db, dir := vaultDB(t), gitRepo(t)
	commitFile(t, dir, "gen.go", "v1\n")
	if _, err := Remember(db, RememberRequest{Memory: memory.Memory{
		Text: "Decided to keep gen.go checked in", Kind: memory.Fact, Source: "manual",
	}}); err != nil {
		t.Fatal(err)
	}
	commitFile(t, dir, "gen.go", "v2\n")
	r, err := Recall(db, RecallQuery{Query: "gen.go checked in", Limit: 5, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Memories) != 1 || r.Memories[0].Drift != nil || strings.Contains(r.Text(""), "⚠") {
		t.Fatalf("a memory with no commit was flagged: %s", r.Text(""))
	}
}
