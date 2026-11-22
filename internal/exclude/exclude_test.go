package exclude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func scratchVault(t *testing.T) string {
	t.Helper()
	v := t.TempDir()
	if err := os.MkdirAll(filepath.Join(v, ".logos"), 0o700); err != nil {
		t.Fatal(err)
	}
	return v
}

// A project exclusion that stopped at the folder's own sessions would leave
// every session started from src/ or a worktree inside it readable.
func TestASubdirectoryOfAnExcludedProjectIsExcluded(t *testing.T) {
	v := scratchVault(t)
	proj := t.TempDir()
	if _, err := AddProject(v, proj); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{proj, filepath.Join(proj, "internal", "cart")} {
		if got := Check(v, Subject{Cwd: cwd, ID: "a"}); got != ReasonProject {
			t.Errorf("session in %s: reason %q, want %q", cwd, got, ReasonProject)
		}
	}
	// A sibling that merely shares the name's prefix is another project.
	if got := Check(v, Subject{Cwd: proj + "-two", ID: "a"}); got != ReasonNoFolder && got != "" {
		t.Errorf("a folder sharing only a name prefix was excluded as %q", got)
	}
	if got := Check(v, Subject{Cwd: proj + "-two", ID: "a"}); got == ReasonProject {
		t.Errorf("%s-two is not inside %s", proj, proj)
	}
}

// With a folder excluded, a session that cannot say where it ran might be that
// folder's; reading it would defeat the exclusion, so it is not read.
func TestASessionWithNoKnownFolderIsSkippedWhileAnExclusionExists(t *testing.T) {
	v := scratchVault(t)
	if got := Check(v, Subject{ID: "a"}); got != "" {
		t.Fatalf("nothing is excluded yet, but a session was skipped: %q", got)
	}
	if err := AddSession(v, "some-other-chat-id"); err != nil {
		t.Fatal(err)
	}
	if got := Check(v, Subject{ID: "a"}); got != "" {
		t.Errorf("a session exclusion does not need the folder, but %q", got)
	}
	if _, err := AddProject(v, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := Check(v, Subject{ID: "a"}); got != ReasonNoFolder {
		t.Errorf("reason %q, want %q", got, ReasonNoFolder)
	}
}

// The vault is truth: exclusions are markdown beside auto.md, so deleting the
// cache must not make an excluded chat readable again.
func TestExclusionsSurviveDeletingTheIndex(t *testing.T) {
	v := scratchVault(t)
	proj := t.TempDir()
	if _, err := AddProject(v, proj); err != nil {
		t.Fatal(err)
	}
	if err := AddSession(v, "0123456789-abcdef"); err != nil {
		t.Fatal(err)
	}
	if _, err := Pause(v, time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	// The cache and everything under .logos goes.
	if err := os.WriteFile(filepath.Join(v, ".logos", "index.db"), []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(v, ".logos")); err != nil {
		t.Fatal(err)
	}
	r, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Projects) != 1 || len(r.Sessions) != 1 || len(r.Pauses) != 1 {
		t.Fatalf("after deleting .logos: %+v", r)
	}
	if got := Check(v, Subject{Cwd: proj, ID: "x"}); got != ReasonProject {
		t.Errorf("reason %q after deleting the index, want %q", got, ReasonProject)
	}
	if _, err := os.Stat(filepath.Join(v, File)); err != nil {
		t.Errorf("exclusions are not a markdown file at the vault root: %v", err)
	}
}

func TestAPausedSessionIsSkippedAndResumeEndsThePauseForLaterSessions(t *testing.T) {
	v := scratchVault(t)
	now := time.Now()
	if _, err := Pause(v, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	during := Subject{ID: "a", Start: now.Add(10 * time.Minute), End: now.Add(20 * time.Minute)}
	if got := checkAt(v, during, now.Add(25*time.Minute)); got != ReasonPaused {
		t.Fatalf("a session active in the window: reason %q, want %q", got, ReasonPaused)
	}
	before := Subject{ID: "b", Start: now.Add(-3 * time.Hour), End: now.Add(-2 * time.Hour)}
	if got := checkAt(v, before, now); got != "" {
		t.Errorf("a session that ended before the pause was skipped: %q", got)
	}
	n, err := Resume(v, now.Add(30*time.Minute))
	if err != nil || n != 1 {
		t.Fatalf("resume ended %d windows, err %v", n, err)
	}
	after := Subject{ID: "c", Start: now.Add(40 * time.Minute), End: now.Add(50 * time.Minute)}
	if got := checkAt(v, after, now.Add(55*time.Minute)); got != "" {
		t.Errorf("a session after resume is still skipped: %q", got)
	}
	// What happened during the pause stays unread: that is what pausing was for.
	if got := checkAt(v, during, now.Add(55*time.Minute)); got != ReasonPaused {
		t.Errorf("resume made the paused session readable: %q", got)
	}
}

func TestAnExclusionIsRemovedByTheNameItWasAddedWith(t *testing.T) {
	v := scratchVault(t)
	proj := t.TempDir()
	if _, err := AddProject(v, proj); err != nil {
		t.Fatal(err)
	}
	if kind, err := Remove(v, proj); err != nil || kind != "project" {
		t.Fatalf("remove: %q, %v", kind, err)
	}
	if _, err := Remove(v, "never-added"); err == nil {
		t.Error("removing a name that was never excluded succeeded silently")
	}
}

func TestTheSkipReceiptSaysHowManyAndWhy(t *testing.T) {
	var k Skips
	if k.Line() != "" {
		t.Errorf("nothing skipped but %q", k.Line())
	}
	k.Add(ReasonProject)
	if k.Line() != "1 session skipped (excluded project)" {
		t.Errorf("one skip: %q", k.Line())
	}
	k.Add(ReasonProject)
	k.Add(ReasonPaused)
	if got := k.Line(); !strings.HasPrefix(got, "3 sessions skipped (") || !strings.Contains(got, "2 excluded project") {
		t.Errorf("three skips: %q", got)
	}
}

func TestProseInTheExclusionFileSurvivesAnEdit(t *testing.T) {
	v := scratchVault(t)
	if _, err := AddProject(v, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path(v))
	if err := os.WriteFile(Path(v), append(b, []byte("\nclient work, keep out\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AddSession(v, "abcdefgh-1"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(Path(v))
	if !strings.Contains(string(b), "client work, keep out") {
		t.Errorf("the person's own words were lost:\n%s", b)
	}
}

func TestAnUnreadableExclusionFileSkipsEverythingRatherThanReadingEverything(t *testing.T) {
	v := scratchVault(t)
	if err := os.WriteFile(Path(v), []byte("- project: /x\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(Path(v), 0o644) })
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	if got := Check(v, Subject{Cwd: "/y", ID: "a"}); got != ReasonUnread {
		t.Errorf("reason %q, want %q", got, ReasonUnread)
	}
}

// checkAt asks a freshly loaded list, as a one-shot caller does.
func checkAt(v string, s Subject, now time.Time) string {
	r, _ := Load(v)
	return r.CheckAt(s, now)
}

func writeList(t *testing.T, v, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(v, File), []byte("---\ntype: logos-exclude\n---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A person writing `- project: ~/x` by hand meant their home folder; the line
// was kept as typed, so it matched no session at all.
func TestAHandWrittenTildeProjectLineExcludesThatFolder(t *testing.T) {
	v := scratchVault(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeList(t, v, "- project: ~/work/x\n")
	if got := Check(v, Subject{Cwd: filepath.Join(home, "work", "x", "sub"), ID: "a"}); got != ReasonProject {
		t.Errorf("~/work/x did not exclude a folder under it: %q", got)
	}
}

func TestAHandWrittenProjectLineThroughASymlinkExcludesTheRealFolder(t *testing.T) {
	v := scratchVault(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here")
	}
	writeList(t, v, "- project: "+link+"\n")
	if got := Check(v, Subject{Cwd: real, ID: "a"}); got != ReasonProject {
		t.Errorf("a symlinked project line did not match the real folder: %q", got)
	}
}

func TestASessionIdTooShortToNameOneChatIsRejected(t *testing.T) {
	v := scratchVault(t)
	err := AddSession(v, "abc")
	if err == nil || !strings.Contains(err.Error(), "8") {
		t.Fatalf("AddSession(abc) = %v, want an error saying how many characters", err)
	}
	if r, _ := Load(v); len(r.Sessions) != 0 {
		t.Errorf("a too-short id was stored: %v", r.Sessions)
	}
}

// A pause line nobody can parse is a protection somebody wrote and Logos
// cannot see; reading everything meanwhile is the failure this list exists to
// prevent.
func TestAPauseLineThatCannotBeReadExcludesEverythingAndSurvivesAnEdit(t *testing.T) {
	v := scratchVault(t)
	writeList(t, v, "- pause: from=tomorrowish until=later\n")
	if got := Check(v, Subject{Cwd: "/anywhere", ID: "some-chat-id"}); got == "" {
		t.Fatal("a broken pause line let a session be read")
	}
	if _, err := AddProject(v, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(v, File))
	if !strings.Contains(string(b), "from=tomorrowish until=later") {
		t.Errorf("editing the list dropped the broken line:\n%s", b)
	}
	r, _ := Load(v)
	if len(r.Broken) != 1 {
		t.Errorf("Broken = %v, want the one line", r.Broken)
	}
}

// A pause that has ended still records what the person did not want read
// during it; pruning it on the next edit made sessions from that time
// readable again.
func TestAnEndedPauseIsNotPrunedByLaterEdits(t *testing.T) {
	v := scratchVault(t)
	then := time.Now().Add(-48 * time.Hour)
	if _, err := Pause(v, time.Hour, then); err != nil {
		t.Fatal(err)
	}
	if _, err := AddProject(v, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	during := Subject{Cwd: t.TempDir(), ID: "a", Start: then.Add(10 * time.Minute), End: then.Add(20 * time.Minute)}
	if got := Check(v, during); got != ReasonPaused {
		t.Errorf("an old pause was pruned: %q", got)
	}
}

// The list is read once per operation: a sweep over two hundred transcripts
// that re-parsed it for each is two hundred reads of a file that did not change.
func TestALoadedListAnswersWithoutReadingTheFileAgain(t *testing.T) {
	v := scratchVault(t)
	proj := t.TempDir()
	if _, err := AddProject(v, proj); err != nil {
		t.Fatal(err)
	}
	r, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(v, File)); err != nil {
		t.Fatal(err)
	}
	if got := r.Check(Subject{Cwd: proj, ID: "a"}); got != ReasonProject {
		t.Errorf("a loaded list consulted the file again: %q", got)
	}
}

func TestASpanThatCoversAPauseIsRefusedByAnIdAndATimeSpanAlone(t *testing.T) {
	v := scratchVault(t)
	now := time.Now()
	if _, err := Pause(v, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	r, _ := Load(v)
	if got := r.CheckSpan("whatever-id", now.Add(-3*time.Hour), now.Add(30*time.Minute)); got != ReasonPaused {
		t.Errorf("a span that straddles the pause: %q", got)
	}
	if got := r.CheckSpan("whatever-id", now.Add(-3*time.Hour), now.Add(-2*time.Hour)); got != "" {
		t.Errorf("a span before the pause: %q", got)
	}
}
