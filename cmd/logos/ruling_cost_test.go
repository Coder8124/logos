package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/activity"
	"github.com/Coder8124/logos/internal/session"
)

const cartRuling = "caching the cart — the cache evicts on every write, so it never hits"

// commitAs commits c and gives its file the stem it would have had at c.TS.
// Commit names a file by the second it ran and checkpoints are ordered by
// name, so three committed in one test second would otherwise order by luck.
func commitAs(t *testing.T, stem string, c *session.Checkpoint) {
	t.Helper()
	commitRulings(t, c)
	from := filepath.Join(vaultPath(), filepath.FromSlash(c.Slug)+".md")
	if err := os.Rename(from, filepath.Join(filepath.Dir(from), stem+"-"+c.Agent+".md")); err != nil {
		t.Fatal(err)
	}
}

// bannerAfterResume runs the real resume into the real SessionStart hook and
// returns the one line the hook tells the agent to relay.
//
// hook is resolved before standIn moves the working directory.
func sessionStartHook(t *testing.T) string {
	t.Helper()
	hook, err := filepath.Abs("../../plugin/hooks/session-start.sh")
	if err != nil {
		t.Fatal(err)
	}
	return hook
}

func bannerAfterResume(t *testing.T, hook string) (banner, pack string) {
	t.Helper()
	pack = captureStdout(t, func() {
		if err := runResume([]string{"shop"}); err != nil {
			t.Fatal(err)
		}
	})
	handoff := filepath.Join(t.TempDir(), "handoff.md")
	if err := os.WriteFile(handoff, []byte(pack), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fakeProgram(t, bin, "logos", `case "$1 ${2-}" in
"--version ") echo "logos 0.4.99" ;;
"project-name "*) echo shop ;;
"plugin autoupdate") true ;;
"resume "*) cat '`+handoff+`' ;;
esac`)
	cmd := exec.Command("/bin/bash", hook)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_PROJECT_DIR=" + t.TempDir(), noFixedInstallDirs}
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook: %v\n%s", err, raw)
	}
	out := string(raw)
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "restored context for") {
			return line, pack
		}
	}
	t.Fatalf("no banner line in the hook's output:\n%s", out)
	return "", ""
}

// "Carrying 1 ruled-out approach" says something was kept and nothing about
// what keeping it saved. The activity log holds the failed commands of the
// session that ruled it out, and earlier checkpoints hold the sessions that hit
// the same dead end before it; the banner says both, in those numbers.
func TestTheRestoreBannerNamesWhatRederivingTheRuledOutApproachesWouldHaveCost(t *testing.T) {
	hook := sessionStartHook(t)
	standIn(t, "shop")
	t.Setenv("LOGOS_EMBED", "off")
	now := time.Now()
	for i, host := range []string{"sess-early-one", "sess-early-two"} {
		commitAs(t, now.Add(-time.Duration(48-i)*time.Hour).Format("20060102-150405"), &session.Checkpoint{
			Project: "shop", Agent: "codex", HostSession: host, Task: "speed up checkout",
			Failed: []string{cartRuling}, TS: now.Add(-time.Duration(48-i) * time.Hour).Unix(),
		})
	}
	if err := activity.SetRecording(vaultPath(), true); err != nil {
		t.Fatal(err)
	}
	for i, e := range []activity.Event{
		{Session: activity.ShortID("sess-current-xyz"), Failed: true},
		{Session: activity.ShortID("sess-current-xyz"), Failed: true},
		{Session: activity.ShortID("sess-current-xyz")},
		{Session: activity.ShortID("sess-current-xyz"), Failed: true},
		{Session: activity.ShortID("sess-someone-else"), Failed: true},
	} {
		e.Kind, e.Tool, e.Project, e.Summary = activity.KindTool, "Bash", "shop", "go test ./cart/..."
		e.TS = now.Add(-time.Duration(30-i) * time.Minute).Unix()
		if err := activity.Append(vaultPath(), e); err != nil {
			t.Fatal(err)
		}
	}
	commitAs(t, now.Add(-time.Minute).Format("20060102-150405"), &session.Checkpoint{
		Project: "shop", Agent: "claude-code", HostSession: "sess-current-xyz", Task: "speed up checkout",
		Failed: []string{cartRuling}, TS: now.Add(-time.Minute).Unix(),
	})

	banner, pack := bannerAfterResume(t, hook)
	want := "Re-deriving it would repeat 3 failed commands in the session that ruled it out, and 2 earlier sessions that hit the same dead end."
	if !strings.Contains(banner, want) {
		t.Errorf("the banner does not name what the ruling saved:\n%s\n\npack:\n%s", banner, pack)
	}
	if !strings.Contains(banner, "carrying 1 ruled-out approach") {
		t.Errorf("the cost line was counted as a ruling of its own:\n%s", banner)
	}
}

// A number the record does not hold is one the user catches the banner
// inventing. With no log for the session there is no command count, not a
// zero; with no earlier session there is no session count; with neither, the
// banner is the bare count it always was.
func TestTheBannerAddsNoCostClaimTheActivityLogCannotSupport(t *testing.T) {
	hook := sessionStartHook(t)
	standIn(t, "shop")
	t.Setenv("LOGOS_EMBED", "off")
	now := time.Now()
	if err := activity.SetRecording(vaultPath(), true); err != nil {
		t.Fatal(err)
	}
	// Logged, but another conversation's: it says nothing about this one.
	if err := activity.Append(vaultPath(), activity.Event{Kind: activity.KindTool, Tool: "Bash", Project: "shop",
		Session: activity.ShortID("sess-unrelated"), Failed: true, TS: now.Add(-time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	commitAs(t, now.Add(-time.Minute).Format("20060102-150405"), &session.Checkpoint{
		Project: "shop", Agent: "claude-code", HostSession: "sess-current-xyz", Task: "speed up checkout",
		Failed: []string{cartRuling}, TS: now.Add(-time.Minute).Unix(),
	})
	banner, pack := bannerAfterResume(t, hook)
	if strings.Contains(banner, "Re-deriving") || strings.Contains(pack, "Re-deriving") {
		t.Errorf("a cost was claimed with no log of the session and no earlier session:\n%s\n\npack:\n%s", banner, pack)
	}

	// One earlier session, still no log of this one: the session count alone.
	commitAs(t, now.Add(-48*time.Hour).Format("20060102-150405"), &session.Checkpoint{
		Project: "shop", Agent: "codex", HostSession: "sess-early-one", Task: "speed up checkout",
		Failed: []string{cartRuling}, TS: now.Add(-48 * time.Hour).Unix(),
	})
	banner, pack = bannerAfterResume(t, hook)
	if !strings.Contains(banner, "Re-deriving it would repeat 1 earlier session that hit the same dead end.") {
		t.Errorf("the earlier session that hit the same dead end is not named:\n%s\n\npack:\n%s", banner, pack)
	}
	if strings.Contains(banner, "failed command") {
		t.Errorf("a failed-command count was claimed for a session the log never saw:\n%s", banner)
	}
}
