package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeLogosForHook stands in for the binary the hook resolves, with the plugin
// check taking as long as body says and resume answering handoff.
func fakeLogosForHook(t *testing.T, autoupdate, notice, handoff string) string {
	t.Helper()
	bin := t.TempDir()
	fakeProgram(t, bin, "logos", `case "$1 ${2-}" in
"--version ") echo "logos 0.4.99" ;;
"project-name "*) echo kestrel-one ;;
"plugin autoupdate")
  if [ "${3-}" = "--notice" ]; then printf '%s' '`+notice+`'; else `+autoupdate+`; fi ;;
"resume "*) printf '%s' '`+handoff+`' ;;
esac`)
	return bin
}

func runSessionStartHook(t *testing.T, bin string) (string, time.Duration) {
	t.Helper()
	hook, err := filepath.Abs("../../plugin/hooks/session-start.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", hook)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_PROJECT_DIR=" + t.TempDir(), noFixedInstallDirs}
	start := time.Now()
	out, err := cmd.CombinedOutput()
	took := time.Since(start)
	if err != nil {
		t.Fatalf("hook: %v\n%s", err, out)
	}
	return string(out), took
}

// The update reaches the network, and a session start that waits on the network
// is a session start that hangs. Claude Code waits for the hook's output, so a
// child still holding its stdout keeps the session waiting even after the hook
// itself returns — which is why the check is detached with both descriptors
// closed rather than merely backgrounded.
func TestASlowPluginCheckDoesNotHoldUpTheSessionStart(t *testing.T) {
	bin := fakeLogosForHook(t, "sleep 30", "", "## Where we left off\nLast checkpoint by **cli**.")
	out, took := runSessionStartHook(t, bin)
	if took > 10*time.Second {
		t.Errorf("the hook took %s waiting on the plugin check", took)
	}
	if !strings.Contains(out, "Where we left off") {
		t.Errorf("the hook lost the handoff:\n%s", out)
	}
}

// An update nobody hears about reads as one that never happened, and the
// session most likely to follow one is a session with nothing else to say —
// a fresh project, an empty vault. Those printed nothing at all.
func TestTheHookAnnouncesAPluginUpdateEvenWithNoHandoff(t *testing.T) {
	notice := "Logos updated its own Claude Code plugin from 0.4.1 to 0.4.4"
	bin := fakeLogosForHook(t, "true", notice, "")
	out, _ := runSessionStartHook(t, bin)
	if !strings.Contains(out, notice) {
		t.Errorf("a session with no handoff never mentioned the update:\n%s", out)
	}
}

// And when there is a handoff, the update rides along with the restore receipt
// rather than arriving as a second block the model announces separately.
func TestTheHookFoldsAPluginUpdateIntoTheRestoreReceipt(t *testing.T) {
	notice := "Logos updated its own Claude Code plugin from 0.4.1 to 0.4.4"
	bin := fakeLogosForHook(t, "true", notice, "## Where we left off\nLast checkpoint by **cli**.")
	out, _ := runSessionStartHook(t, bin)
	if !strings.Contains(out, notice) {
		t.Errorf("the restore receipt never mentioned the update:\n%s", out)
	}
	if strings.Count(out, notice) != 1 {
		t.Errorf("the update is announced %d times:\n%s", strings.Count(out, notice), out)
	}
}

// pluginReleaseVersion is the version this checkout's plugin manifest states,
// which is the minimum logos its hooks were written against. Read rather than
// written down, so the test moves with every release instead of going stale.
func pluginReleaseVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../plugin/.claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Version == "" {
		t.Fatalf("the plugin manifest states no version (%v):\n%s", err, raw)
	}
	return manifest.Version
}

// fakeLogosAtVersion is a logos that reports version and answers the hook's
// questions with nothing, or with handoff for resume.
func fakeLogosAtVersion(t *testing.T, dir, version, handoff string) {
	t.Helper()
	fakeProgram(t, dir, "logos", `case "$1" in
--version) echo "logos `+version+` darwin/arm64 go1.26" ;;
project-name) echo kestrel-one ;;
resume) printf '%s' '`+handoff+`' ;;
esac`)
}

// The hooks pass every guard as a flag or a variable, and a logos too old for
// one ignores it: the plugin's newer features went off one by one, and nothing
// the user would read said so. So a binary older than its plugin is named, with
// both versions and the command that fixes it, in the block the agent is told
// to relay — and said once even when the restore receipt is printed too.
func TestALogosOlderThanItsPluginIsAnnounced(t *testing.T) {
	plugin := pluginReleaseVersion(t)
	for _, handoff := range []string{"", "## Where we left off\nLast checkpoint by **cli**."} {
		bin := t.TempDir()
		fakeLogosAtVersion(t, bin, "0.0.1", handoff)
		out, _ := runSessionStartHook(t, bin)
		for _, want := range []string{
			"plugin is Logos " + plugin, filepath.Join(bin, "logos") + ") is 0.0.1",
			"brew upgrade logos-mcp", "npm i -g @noeton/logos@latest",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("handoff %q: the session start never said %q:\n%s", handoff, want, out)
			}
		}
		if n := strings.Count(out, "brew upgrade logos-mcp"); n != 1 {
			t.Errorf("handoff %q: the old binary is announced %d times:\n%s", handoff, n, out)
		}
		if !strings.Contains(out, "FIRST, tell the user") {
			t.Errorf("handoff %q: the notice is not in the block the agent relays:\n%s", handoff, out)
		}
	}
}

// The other direction is the ordinary state between `brew upgrade` and the
// plugin's own autoupdate, which closes it unasked and which doctor already
// reports. Saying it every session would teach the user to skim past the line
// that matters.
func TestALogosNewerThanItsPluginIsNotAnnounced(t *testing.T) {
	bin := t.TempDir()
	fakeLogosAtVersion(t, bin, "99.0.0", "")
	out, _ := runSessionStartHook(t, bin)
	if strings.TrimSpace(out) != "" {
		t.Errorf("a binary newer than its plugin, with nothing else to say, printed:\n%s", out)
	}
}
