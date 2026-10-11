package main

import (
	"os"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/auto"
)

// Asking what auto is must not answer by turning on dream, which spends a
// local model's time and is off until somebody says otherwise.
func TestABareLogosAutoShowsTheSwitchesAndChangesNothing(t *testing.T) {
	standIn(t, "shop")
	out := captureStdout(t, func() {
		if err := runAuto(nil); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Stat(auto.Path(vaultPath())); !os.IsNotExist(err) {
		t.Errorf("a bare logos auto wrote %s (stat: %v)", auto.File, err)
	}
	if !strings.Contains(out, "dream") {
		t.Errorf("a bare logos auto did not show the switches:\n%s", out)
	}
}

func TestAutoConfigOnAnUnreadableAutoFileFails(t *testing.T) {
	standIn(t, "shop")
	// A directory where the file should be: unreadable as a file on every OS
	// and for every user, root included.
	if err := os.Mkdir(auto.Path(vaultPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := runAuto([]string{"config"}); err == nil {
			t.Error("logos auto config succeeded on an auto.md it could not read")
		}
	})
}
