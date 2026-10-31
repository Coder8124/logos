package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scratchRepo is a throwaway git repository holding a Go module, for driving
// the checks over commits written to order.
type scratchRepo struct {
	t   *testing.T
	dir string
}

func newScratchRepo(t *testing.T) *scratchRepo {
	t.Helper()
	r := &scratchRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q")
	r.git("config", "maintenance.auto", "false")
	r.write("go.mod", "module scratch\n\ngo 1.21\n")
	r.commit("Base")
	return r
}

// isolatedGit is this process's environment without the caller's git config: a
// contributor with commit.gpgsign or a global hooks path would otherwise fail
// these tests at the first commit, for nothing to do with the scripts.
func isolatedGit() []string {
	return append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
}

func (r *scratchRepo) git(args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(isolatedGit(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (r *scratchRepo) write(name, body string) {
	r.t.Helper()
	p := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *scratchRepo) commit(msg string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", msg)
}

// check runs one of the scripts over the last commit, and reports whether it
// passed it.
func (r *scratchRepo) check(script string) (bool, string) {
	r.t.Helper()
	abs, err := filepath.Abs(script)
	if err != nil {
		r.t.Fatal(err)
	}
	cmd := exec.Command("bash", abs, "HEAD~1", "HEAD")
	cmd.Dir = r.dir
	cmd.Env = isolatedGit()
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

const buggyAdd = "package scratch\n\nfunc Add(a, b int) int { return a - b }\n"
const fixedAdd = "package scratch\n\nfunc Add(a, b int) int { return a + b }\n"
const sumsTest = "package scratch\n\nimport \"testing\"\n\nfunc TestAddSumsItsArguments(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(Add(2, 2))\n\t}\n}\n"

func TestABugfixWhoseTestFailsOnTheOldCodeIsAccepted(t *testing.T) {
	r := newScratchRepo(t)
	r.write("add.go", buggyAdd)
	r.commit("Add adds")
	r.write("add.go", fixedAdd)
	r.write("add_test.go", sumsTest)
	r.commit("BUG: Add sums its arguments")

	ok, out := r.check("check-bug-tests.sh")
	if !ok || !strings.Contains(out, "TestAddSumsItsArguments fails without the fix") {
		t.Errorf("a proved bugfix was rejected:\n%s", out)
	}
}

// The rule the script exists for: a test that passes on the old code is not
// testing the fix, however well it is named.
func TestABugfixWhoseTestPassesOnTheOldCodeIsRejected(t *testing.T) {
	r := newScratchRepo(t)
	r.write("add.go", buggyAdd)
	r.commit("Add adds")
	r.write("add.go", fixedAdd)
	r.write("add_test.go", "package scratch\n\nimport \"testing\"\n\nfunc TestAddOfZeroAndZeroIsZero(t *testing.T) {\n\tif Add(0, 0) != 0 {\n\t\tt.Fatal(Add(0, 0))\n\t}\n}\n")
	r.commit("BUG: Add sums its arguments")

	ok, out := r.check("check-bug-tests.sh")
	if ok || !strings.Contains(out, "passes without the fix") {
		t.Errorf("a test that cannot fail was accepted as proof:\n%s", out)
	}
}

func TestABugfixWithNoTestIsRejected(t *testing.T) {
	r := newScratchRepo(t)
	r.write("add.go", buggyAdd)
	r.commit("Add adds")
	r.write("add.go", fixedAdd)
	r.commit("BUG: Add sums its arguments")

	ok, out := r.check("check-bug-tests.sh")
	if ok || !strings.Contains(out, "adds or changes no test") {
		t.Errorf("a bugfix with no test was accepted:\n%s", out)
	}
}

// A flaky test is a bug too, and its fix changes only the test: there is no
// old code to put back, so there is nothing to fail.
func TestABugfixThatChangesOnlyATestIsAccepted(t *testing.T) {
	r := newScratchRepo(t)
	r.write("add.go", fixedAdd)
	r.write("add_test.go", "package scratch\n\nimport \"testing\"\n\nfunc helper(*testing.T) int { return 1 }\n")
	r.commit("Add adds")
	r.write("add_test.go", "package scratch\n\nimport \"testing\"\n\nfunc helper(*testing.T) int { return 2 }\n")
	r.commit("BUG: The helper returns two")

	ok, out := r.check("check-bug-tests.sh")
	if !ok || !strings.Contains(out, "fixes only tests") {
		t.Errorf("a fix to a test itself was rejected:\n%s", out)
	}
}

// A deadlock or an os.Exit in the old code prints no "--- FAIL" line, only the
// package's FAIL; a fix for one was rejected as if its test had not run.
func TestABugfixWhoseOldCodeKillsTheTestBinaryIsAccepted(t *testing.T) {
	r := newScratchRepo(t)
	r.write("add.go", "package scratch\n\nimport \"os\"\n\nfunc Add(a, b int) int { os.Exit(1); return 0 }\n")
	r.commit("Add adds")
	r.write("add.go", fixedAdd)
	r.write("add_test.go", sumsTest)
	r.commit("BUG: Add returns instead of exiting")

	ok, out := r.check("check-bug-tests.sh")
	if !ok || !strings.Contains(out, "dies without the fix") {
		t.Errorf("a fix for an exit in the old code was rejected:\n%s", out)
	}
}

// A test that skips itself here — no model runtime, the wrong OS — proves
// nothing either way, and saying it passes without the fix sends its author
// looking for a mistake that is not there.
func TestABugfixWhoseTestSkipsHereIsReportedAsUnproved(t *testing.T) {
	r := newScratchRepo(t)
	r.write("add.go", buggyAdd)
	r.commit("Add adds")
	r.write("add.go", fixedAdd)
	r.write("add_test.go", "package scratch\n\nimport \"testing\"\n\nfunc TestAddSumsItsArguments(t *testing.T) {\n\tt.Skip(\"needs a model runtime\")\n}\n")
	r.commit("BUG: Add sums its arguments")

	ok, out := r.check("check-bug-tests.sh")
	if !ok || !strings.Contains(out, "unproved") || strings.Contains(out, "passes without the fix") {
		t.Errorf("a skipped test was not reported as unproved:\n%s", out)
	}
}

// A diff hunk is labelled with the function above it, so a test appended after
// another one named that neighbour too. The neighbour passed, the new test
// skipped, and a case-insensitive-disk fix proved on macOS was rejected on
// Linux as passing without the fix instead of reported as unproved.
func TestATestAddedBelowAnotherIsNotJudgedByItsNeighbour(t *testing.T) {
	r := newScratchRepo(t)
	r.write("add.go", buggyAdd)
	r.write("add_test.go", "package scratch\n\nimport \"testing\"\n\nfunc TestAddOfZeroAndZeroIsZero(t *testing.T) {\n\tif Add(0, 0) != 0 {\n\t\tt.Fatal(Add(0, 0))\n\t}\n}\n")
	r.commit("Add adds")
	r.write("add.go", fixedAdd)
	r.write("add_test.go", "package scratch\n\nimport \"testing\"\n\nfunc TestAddOfZeroAndZeroIsZero(t *testing.T) {\n\tif Add(0, 0) != 0 {\n\t\tt.Fatal(Add(0, 0))\n\t}\n}\n\nfunc TestAddSumsItsArguments(t *testing.T) {\n\tt.Skip(\"the wrong OS\")\n}\n")
	r.commit("BUG: Add sums its arguments")

	ok, out := r.check("check-bug-tests.sh")
	if !ok || !strings.Contains(out, "unproved") || strings.Contains(out, "TestAddOfZeroAndZeroIsZero") {
		t.Errorf("a new test was judged by the passing test above it:\n%s", out)
	}
}

// Only the chaos tag was passed, so a test behind any other — race_on_test.go
// is behind `race` — never ran, and its fix was rejected.
func TestABugfixWhoseTestIsBehindABuildTagIsRun(t *testing.T) {
	r := newScratchRepo(t)
	r.write("add.go", buggyAdd)
	r.commit("Add adds")
	r.write("add.go", fixedAdd)
	r.write("add_test.go", "//go:build slow\n\n"+sumsTest)
	r.commit("BUG: Add sums its arguments")

	ok, out := r.check("check-bug-tests.sh")
	if !ok || !strings.Contains(out, "TestAddSumsItsArguments fails without the fix") {
		t.Errorf("a test behind a build tag was not run:\n%s", out)
	}
}

func TestATestNamedWithALabelIsRejectedAndASentenceIsNot(t *testing.T) {
	for name, want := range map[string]bool{
		"TestForget":                            false,
		"TestMCPServer":                         false,
		"TestAForgottenIDIsNeverHandedOutAgain": true,
		"TestNoHitsRendersNothing":              true,
	} {
		r := newScratchRepo(t)
		r.write("x_test.go", "package scratch\n\nimport \"testing\"\n\nfunc "+name+"(t *testing.T) {}\n")
		r.commit("A test")
		if ok, out := r.check("check-test-names.sh"); ok != want {
			t.Errorf("%s: passed %v, want %v:\n%s", name, ok, want, out)
		}
	}
}

// The labels from before the rule are left alone, and moving one into another
// file is not adding it — though the diff shows it as a new function there.
func TestMovingAnOldLabelToAnotherFileDoesNotFlagIt(t *testing.T) {
	r := newScratchRepo(t)
	r.write("old_test.go", "package scratch\n\nimport \"testing\"\n\nfunc TestFullDisk(t *testing.T) {}\n")
	r.write("disk_test.go", "package scratch\n\nimport \"testing\"\n\nfunc TestAFullDiskIsReportedNotSwallowed(t *testing.T) {}\n")
	r.commit("Old tests")
	r.git("rm", "-q", "old_test.go")
	r.write("disk_test.go", "package scratch\n\nimport \"testing\"\n\nfunc TestAFullDiskIsReportedNotSwallowed(t *testing.T) {}\n\nfunc TestFullDisk(t *testing.T) {}\n")
	r.commit("The disk tests live together")

	if ok, out := r.check("check-test-names.sh"); !ok {
		t.Errorf("a moved label was flagged as new:\n%s", out)
	}
}
