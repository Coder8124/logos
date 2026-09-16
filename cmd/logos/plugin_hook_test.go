package main

import (
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
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_PROJECT_DIR=" + t.TempDir()}
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
