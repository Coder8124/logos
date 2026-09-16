package setup

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installedPlugin writes the records Claude Code keeps for a plugin installed
// at user scope and left enabled, which is the only shape that counts as the
// plugin connecting.
func installedPlugin(t *testing.T, home, version string) {
	t.Helper()
	writeClaudeFile(t, home, "plugins/installed_plugins.json",
		`{"version":2,"plugins":{"logos@logos":[{"scope":"user","version":"`+version+`"}]}}`)
}

// stubUpdate replaces the one side effect, so these tests decide whether an
// update should happen on a machine with no Claude Code on it.
func stubUpdate(t *testing.T, err error) *int {
	t.Helper()
	runs := 0
	prev := updatePlugin
	updatePlugin = func() error {
		runs++
		return err
	}
	t.Cleanup(func() { updatePlugin = prev })
	return &runs
}

// The whole point: nobody has to notice the plugin is old. `logos update`
// replaces the binary and leaves the plugin where it was, so an install that
// started on 0.1.2 ran 0.1.2's hooks against a 0.4 server forever.
func TestThePluginUpdatesItselfWhenItIsOlderThanTheBinary(t *testing.T) {
	home := fakeHome(t)
	installedPlugin(t, home, "0.4.1")
	runs := stubUpdate(t, nil)

	updated, err := AutoUpdatePlugin("v0.4.4", time.Now())
	if err != nil || !updated {
		t.Fatalf("AutoUpdatePlugin = %v, %v; want an update", updated, err)
	}
	if *runs != 1 {
		t.Errorf("ran the update %d times, want 1", *runs)
	}
}

// A session start has to stay instant, and the update reaches the network. One
// check a day is enough for something released less often than that.
func TestThePluginIsNotCheckedMoreThanOnceADay(t *testing.T) {
	home := fakeHome(t)
	installedPlugin(t, home, "0.4.1")
	runs := stubUpdate(t, nil)

	now := time.Now()
	if _, err := AutoUpdatePlugin("v0.4.4", now); err != nil {
		t.Fatal(err)
	}
	if _, err := AutoUpdatePlugin("v0.4.4", now.Add(AutoUpdateEvery-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if *runs != 1 {
		t.Errorf("checked %d times within a day, want 1", *runs)
	}
	if _, err := AutoUpdatePlugin("v0.4.4", now.Add(AutoUpdateEvery+time.Minute)); err != nil {
		t.Fatal(err)
	}
	if *runs != 2 {
		t.Errorf("did not check again after a day: %d runs", *runs)
	}
}

// A plugin level with the binary, or newer than it, or carrying no release
// number to rank at all, is not a plugin to reinstall on every check.
func TestAnUpToDatePluginIsNotUpdated(t *testing.T) {
	for _, plugin := range []string{"0.4.4", "0.5.0", "dev"} {
		t.Run(plugin, func(t *testing.T) {
			home := fakeHome(t)
			installedPlugin(t, home, plugin)
			runs := stubUpdate(t, nil)

			updated, err := AutoUpdatePlugin("v0.4.4", time.Now())
			if err != nil || updated {
				t.Fatalf("AutoUpdatePlugin = %v, %v; want nothing done", updated, err)
			}
			if *runs != 0 {
				t.Errorf("updated a plugin at %s anyway", plugin)
			}
			if n := UpdateNotice(); n != "" {
				t.Errorf("announced %q for a plugin that was not updated", n)
			}
		})
	}
}

// A plugin Claude Code does not load — disabled, or installed for one project
// — is a problem doctor names; updating it in the background would fix nothing
// and would reach the network every day to do it.
func TestAPluginClaudeCodeDoesNotLoadIsNotUpdated(t *testing.T) {
	home := fakeHome(t)
	installedPlugin(t, home, "0.4.1")
	writeClaudeFile(t, home, "settings.json", `{"enabledPlugins":{"logos@logos":false}}`)
	runs := stubUpdate(t, nil)

	if updated, err := AutoUpdatePlugin("v0.4.4", time.Now()); updated || err != nil {
		t.Fatalf("AutoUpdatePlugin = %v, %v; want nothing done", updated, err)
	}
	if *runs != 0 {
		t.Error("updated a plugin Claude Code never loads")
	}
}

// Background work nobody sees reads as a broken product: the session after the
// update opens saying what happened. Once — a week-old update announced every
// session is noise, and reads as though it just happened.
func TestASessionSaysThePluginUpdatedItselfAndSaysItOnlyOnce(t *testing.T) {
	home := fakeHome(t)
	installedPlugin(t, home, "0.4.1")
	stubUpdate(t, nil)

	if _, err := AutoUpdatePlugin("v0.4.4", time.Now()); err != nil {
		t.Fatal(err)
	}
	notice := UpdateNotice()
	if !strings.Contains(notice, "0.4.1") || !strings.Contains(notice, "0.4.4") {
		t.Errorf("notice does not name both versions: %q", notice)
	}
	if again := UpdateNotice(); again != "" {
		t.Errorf("announced the same update twice: %q", again)
	}
}

// A failed update that returns a success-shaped result is the worst outcome
// available here: the plugin stays old and the user is told nothing, daily.
func TestAFailedPluginUpdateIsReportedNotSwallowed(t *testing.T) {
	home := fakeHome(t)
	installedPlugin(t, home, "0.4.1")
	stubUpdate(t, errors.New("`claude plugin update logos@logos` did not finish in two minutes"))

	updated, err := AutoUpdatePlugin("v0.4.4", time.Now())
	if updated || err == nil {
		t.Fatalf("AutoUpdatePlugin = %v, %v; want the failure back", updated, err)
	}
	notice := UpdateNotice()
	if !strings.Contains(notice, "did not finish in two minutes") {
		t.Errorf("the failure was not announced: %q", notice)
	}
	if !strings.Contains(notice, "claude plugin marketplace update logos") {
		t.Errorf("the notice does not say how to do it by hand: %q", notice)
	}
}

// The stamp is the one piece of durable state these tests share, and it does
// not live under HOME on Linux: os.UserConfigDir answers XDG_CONFIG_HOME first,
// and GitHub's ubuntu image exports it. So a fake home that moved only HOME
// left every test in this package reading one real stamp file — four of them
// failed on CI while passing on macOS, and the suite wrote into the developer's
// own ~/.config/logos on the way. The decoy stands in for CI's environment.
func TestThePluginUpdateStampStaysInsideTheFakeHome(t *testing.T) {
	decoy := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", decoy)
	home := fakeHome(t)

	p, err := updateStampPath()
	if err != nil {
		t.Fatal(err)
	}
	if under(p, decoy) {
		t.Fatalf("the update stamp is written to %s, outside the fake home — on a real machine that is the developer's own config directory", p)
	}
	if !under(p, home) {
		t.Errorf("the update stamp is at %s, which is not under the fake home %s", p, home)
	}
}

// under is separator-anchored, so a sibling temp directory whose name merely
// starts with the same characters does not read as being inside it.
func under(path, dir string) bool {
	return strings.HasPrefix(path, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}
