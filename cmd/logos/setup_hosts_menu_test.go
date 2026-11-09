package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/setup"
)

// recordingHosts are detected hosts whose registration is written down
// instead of done.
func recordingHosts(t *testing.T, names ...string) *[]string {
	t.Helper()
	var wired []string
	var hosts []setup.Host
	for _, name := range names {
		hosts = append(hosts, setup.Host{
			Name:   name,
			Detect: func() bool { return true },
			Where:  func() string { return "/nowhere/config.json" },
			Register: func(setup.Server) (setup.Outcome, error) {
				wired = append(wired, name)
				return setup.Registered, nil
			},
		})
	}
	fakeHosts(t)
	old := detectHosts
	detectHosts = func() []setup.Host { return hosts }
	t.Cleanup(func() { detectHosts = old })
	return &wired
}

// untick answers the host menu: it unticks the given rows and submits.
func untick(t *testing.T, res pick, rows ...int) *[]menu {
	t.Helper()
	var shown []menu
	was := pickOnTerminal
	pickOnTerminal = func(m menu) (int, pick, int, error) {
		shown = append(shown, m)
		for _, r := range rows {
			m.checked[r] = false
		}
		return len(m.rows), res, len(m.rows) + 2, nil
	}
	t.Cleanup(func() { pickOnTerminal = was })
	return &shown
}

func TestSetupAtATerminalWiresOnlyTheHostsLeftTicked(t *testing.T) {
	dir := setupInFakeHome(t)
	wired := recordingHosts(t, "Fakey Cursor", "Fakey Codex", "Fakey Zed")
	atTerminal(t, true)
	shown := untick(t, picked, 1)

	var err error
	out := captureStdout(t, func() { _, err = wireHosts(dir, wireOpts{}) })
	if err != nil {
		t.Fatalf("wireHosts: %v\n%s", err, out)
	}
	if len(*shown) != 1 {
		t.Fatalf("the host menu opened %d times", len(*shown))
	}
	for i, c := range (*shown)[0].checked {
		if !c && i != 1 {
			t.Fatalf("row %d opened unticked; every host found starts ticked", i)
		}
	}
	if !reflect.DeepEqual(*wired, []string{"Fakey Cursor", "Fakey Zed"}) {
		t.Fatalf("wired %v, want the two left ticked", *wired)
	}
	if !strings.Contains(out, "Fakey Codex") || !strings.Contains(out, "left alone: unticked") {
		t.Fatalf("the unticked host was not named:\n%s", out)
	}
	if strings.Contains(out, "[Y/n]") {
		t.Fatalf("the menu asked [Y/n] as well:\n%s", out)
	}
}

func TestCancellingTheSetupHostMenuWiresNothing(t *testing.T) {
	dir := setupInFakeHome(t)
	wired := recordingHosts(t, "Fakey Cursor", "Fakey Codex")
	atTerminal(t, true)
	untick(t, cancelled)

	var err error
	out := captureStdout(t, func() { _, err = wireHosts(dir, wireOpts{}) })
	if err != nil {
		t.Fatalf("wireHosts: %v\n%s", err, out)
	}
	if len(*wired) != 0 {
		t.Fatalf("a cancel wired %v", *wired)
	}
	if !strings.Contains(out, "skipped") {
		t.Fatalf("the cancel was not said:\n%s", out)
	}
	// The --host line was the [Y/n] prompt's only way to a subset; after a
	// checklist it sends the person to type what the menu just offered.
	if strings.Contains(out, "--host") {
		t.Fatalf("a cancelled checklist still teaches --host:\n%s", out)
	}
}

// When the menu could not be drawn, setup fell back to [Y/n] and printed the
// whole roster again, so every host that is not installed was listed twice.
func TestAHostMenuThatCannotOpenListsEachMissingHostOnce(t *testing.T) {
	dir := setupInFakeHome(t)
	recordingHosts(t, "Fakey Cursor")
	gone := setup.Host{Name: "Fakey Absent", Detect: func() bool { return false }, Where: func() string { return "" }}
	withGone := detectHosts
	detectHosts = func() []setup.Host { return append(withGone(), gone) }
	atTerminal(t, true)
	was := pickOnTerminal
	pickOnTerminal = func(menu) (int, pick, int, error) { return 0, cancelled, 0, errors.New("no raw mode") }
	t.Cleanup(func() { pickOnTerminal = was })

	out := captureStdout(t, func() { wireHosts(dir, wireOpts{}) })
	if !strings.Contains(out, "asking instead") {
		t.Fatalf("the fallback was not taken:\n%s", out)
	}
	if n := strings.Count(out, "Fakey Absent"); n != 1 {
		t.Fatalf("the missing host is listed %d times:\n%s", n, out)
	}
	if !strings.Contains(out, "Fakey Cursor") {
		t.Fatalf("the fallback prompt does not say what it would wire:\n%s", out)
	}
}

func TestSetupWithYesNeverOpensTheHostMenu(t *testing.T) {
	dir := setupInFakeHome(t)
	wired := recordingHosts(t, "Fakey Cursor", "Fakey Codex")
	atTerminal(t, true)
	shown := untick(t, picked, 0)

	captureStdout(t, func() { wireHosts(dir, wireOpts{yes: true}) })
	if len(*shown) != 0 || len(*wired) != 2 {
		t.Fatalf("--yes opened %d menus and wired %v", len(*shown), *wired)
	}
}
