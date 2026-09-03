package announce

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultIsOn(t *testing.T) {
	t.Setenv(Env, "")
	if got := Setting(t.TempDir()); got != On {
		t.Errorf("a vault that has never been configured should announce: got %v", got)
	}
}

func TestEnvironmentOverridesTheStoredSetting(t *testing.T) {
	v := t.TempDir()
	if err := Store(v, Off); err != nil {
		t.Fatal(err)
	}
	t.Setenv(Env, "on")
	if got := Setting(v); got != On {
		t.Errorf("an explicitly set variable should win over the file: got %v", got)
	}
}

func TestStoredSettingSurvives(t *testing.T) {
	t.Setenv(Env, "")
	v := t.TempDir()
	for _, l := range []Level{Off, Quiet} {
		if err := Store(v, l); err != nil {
			t.Fatal(err)
		}
		if got := Setting(v); got != l {
			t.Errorf("stored %v, read back %v", l, got)
		}
	}
}

// Back to the default should leave no trace, so a configured-then-reset vault
// and a fresh one are indistinguishable.
func TestStoringOnRemovesTheFile(t *testing.T) {
	t.Setenv(Env, "")
	v := t.TempDir()
	Store(v, Off)
	if err := Store(v, On); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(v, File)); !os.IsNotExist(err) {
		t.Error("returning to the default should remove the file, not write \"on\" into it")
	}
	if got := Setting(v); got != On {
		t.Errorf("got %v", got)
	}
}

func TestUnparseableSettingFallsBackRatherThanBreaking(t *testing.T) {
	t.Setenv(Env, "loud please")
	if got := Setting(t.TempDir()); got != On {
		t.Errorf("nonsense should land on the default, not turn Logos off: got %v", got)
	}
}

func TestLevelsShapeTheLine(t *testing.T) {
	if got := At(On, "stored in brain — memory #1"); !strings.HasPrefix(got, Marker) {
		t.Errorf("on should carry the marker: %q", got)
	}
	if got := At(Quiet, "stored in brain — memory #1"); got != "stored in brain — memory #1" {
		t.Errorf("quiet should be the bare fact: %q", got)
	}
	if got := At(Off, "stored in brain — memory #1"); got != "" {
		t.Errorf("off should be empty so the caller can decide: %q", got)
	}
}

func TestNothingToSaySaysNothing(t *testing.T) {
	if got := At(On, "   "); got != "" {
		t.Errorf("an empty receipt should not become a bare marker: %q", got)
	}
}
