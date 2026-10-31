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

// sessionStartStreams runs the SessionStart hook and keeps its two streams
// apart: stdout is what Claude Code hands the agent to relay, stderr is the
// detail only a debug log shows, and the review of #80 was about which is which.
func sessionStartStreams(t *testing.T, bin string, env ...string) (stdout, stderr string) {
	t.Helper()
	hook, err := filepath.Abs("../../plugin/hooks/session-start.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", hook)
	cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_PROJECT_DIR=" + t.TempDir(), noFixedInstallDirs}, env...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("hook: %v\n%s%s", err, out.String(), errb.String())
	}
	return out.String(), errb.String()
}

// The hooks pass every guard as a flag or a variable, and a logos a minor
// release too old for one ignores it: the plugin's newer features went off one
// by one, and nothing the user would read said so. So that gap is said in the
// block the agent is told to relay, once even beside the restore receipt — and
// in a line short enough to relay, with the path the agent has no use for on
// stderr instead.
func TestALogosOlderThanItsPluginIsAnnounced(t *testing.T) {
	plugin := pluginReleaseVersion(t)
	for _, handoff := range []string{"", "## Where we left off\nLast checkpoint by **cli**."} {
		bin := t.TempDir()
		fakeLogosAtVersion(t, bin, "0.0.1", handoff)
		out, detail := sessionStartStreams(t, bin)
		for _, want := range []string{
			"Logos 0.0.1", "its " + plugin + " plugin", "brew upgrade logos-mcp", "npm i -g @noeton/logos@latest",
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
		if strings.Contains(out, bin) || !strings.Contains(detail, filepath.Join(bin, "logos")) {
			t.Errorf("handoff %q: the binary's path belongs on stderr, not in the relayed line:\nstdout: %s\nstderr: %s", handoff, out, detail)
		}
		if handoff == "" {
			_, relay, _ := strings.Cut(out, "(they cannot see this block):")
			if n := len(strings.Fields(relay)); n > 30 {
				t.Errorf("the line the agent is told to relay in one short line is %d words:\n%s", n, relay)
			}
		}
	}
}

// At or above the plugin's version there is nothing to say: newer is the
// ordinary state between `brew upgrade` and the plugin's own autoupdate, which
// closes it unasked and which doctor already reports, and saying it every
// session would teach the user to skim past the line that matters. "v0.5.5" is
// the same release as "0.5.5" (#173 was a v-prefixed version ranked as junk).
func TestALogosAtOrAboveItsPluginsVersionIsNotAnnounced(t *testing.T) {
	plugin := pluginReleaseVersion(t)
	for _, version := range []string{plugin, "v" + plugin, "99.0.0"} {
		bin := t.TempDir()
		fakeLogosAtVersion(t, bin, version, "")
		out, _ := sessionStartStreams(t, bin)
		for _, never := range []string{"older than", "features are off", "brew upgrade", "LOGOS_BIN"} {
			if strings.Contains(out, never) {
				t.Errorf("logos %s against a %s plugin said %q:\n%s", version, plugin, never, out)
			}
		}
		if strings.TrimSpace(out) != "" {
			t.Errorf("logos %s against a %s plugin, with nothing else to say, printed:\n%s", version, plugin, out)
		}
	}
}

// A LOGOS_BIN with a typo and no other copy installed is the likeliest way to
// get it wrong, and the hook exited the moment nothing resolved — before the
// notice that names LOGOS_BIN — so the session simply had no Logos and said
// nothing about why.
func TestALogosBinThatDoesNotRunIsAnnouncedEvenWhenNothingElseRuns(t *testing.T) {
	out, _ := sessionStartStreams(t, t.TempDir(), "LOGOS_BIN=/nowhere/logos")
	if !strings.Contains(out, "FIRST, tell the user") || !strings.Contains(out, "LOGOS_BIN") {
		t.Errorf("a LOGOS_BIN that does not run, with nothing else to run, was silent:\n%q", out)
	}
	if strings.Contains(out, "another copy") {
		t.Errorf("the notice claimed another copy runs when none does:\n%s", out)
	}
	if n := strings.Count(strings.TrimSpace(out), "\n"); n != 0 {
		t.Errorf("want one line, got %d:\n%s", n+1, out)
	}
}
