package auto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAVaultNobodyAskedDoesWhatLogosDidBeforeTheSwitches(t *testing.T) {
	vault := t.TempDir()
	for _, sp := range specs {
		if got := On(vault, sp.name); got != sp.today {
			t.Errorf("%s is %v in a vault with no auto.md; before the switch it was %v", sp.name, got, sp.today)
		}
	}
}

func TestAutoOnLeavesASwitchTheUserTurnedOffOff(t *testing.T) {
	vault := t.TempDir()
	if err := Set(vault, "checkpoint", false); err != nil {
		t.Fatal(err)
	}
	if err := SetMaster(vault, true); err != nil {
		t.Fatal(err)
	}
	if On(vault, Checkpoint) {
		t.Error("auto on turned back on a switch the user had turned off by hand")
	}
	if !On(vault, Distil) {
		t.Error("auto on left a switch nobody set by hand off")
	}
}

func TestAutoOffTurnsOffEverySwitchNotSetByHand(t *testing.T) {
	vault := t.TempDir()
	if err := Set(vault, "distil", true); err != nil {
		t.Fatal(err)
	}
	if err := SetMaster(vault, false); err != nil {
		t.Fatal(err)
	}
	for _, s := range Switches() {
		want := s == Distil
		if On(vault, s) != want {
			t.Errorf("after auto off, %s is %v; want %v", s, On(vault, s), want)
		}
	}
}

func TestAutoConfigSurvivesDeletingTheIndex(t *testing.T) {
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, ".logos"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SetMaster(vault, false); err != nil {
		t.Fatal(err)
	}
	if err := Set(vault, "rulings-at-edit", true); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(vault, ".logos")); err != nil {
		t.Fatal(err)
	}
	if On(vault, Checkpoint) || !On(vault, RulingsAtEdit) {
		t.Error("deleting .logos/ changed what auto.md says")
	}
}

func TestNoConfigMakesAVerifyCommandRunAutomatically(t *testing.T) {
	vault := t.TempDir()
	for _, f := range Fixed {
		if err := Set(vault, f.Name, true); err == nil || !strings.Contains(err.Error(), "never automatic") {
			t.Errorf("logos auto config %s on was accepted: %v", f.Name, err)
		}
	}
	if err := os.WriteFile(Path(vault), []byte("auto: on\n- verify: on\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Problems) != 1 || !strings.Contains(c.Problems[0], "verify is never automatic") {
		t.Errorf("a hand-written verify switch was not answered: %v", c.Problems)
	}
	for _, s := range Switches() {
		if strings.Contains(string(s), "verify") {
			t.Errorf("%s is a switch that would run vault text", s)
		}
	}
}

func TestEditingTheSwitchesKeepsWhatTheUserWroteInTheFile(t *testing.T) {
	vault := t.TempDir()
	hand := "# mine\n\nI keep checkpoints off on this laptop.\n- checkpoint: off\n"
	if err := os.WriteFile(Path(vault), []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetMaster(vault, true); err != nil {
		t.Fatal(err)
	}
	if err := Set(vault, "distil", false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(Path(vault))
	got := string(raw)
	for _, want := range []string{"I keep checkpoints off on this laptop.", "- checkpoint: off", "auto: on", "- distil: off"} {
		if !strings.Contains(got, want) {
			t.Errorf("auto.md lost %q:\n%s", want, got)
		}
	}
	if On(vault, Checkpoint) || On(vault, Distil) || !On(vault, RulingsAtEdit) {
		t.Errorf("the edited file reads back wrong:\n%s", got)
	}
}

func TestALineThatMeansNothingIsReportedNotDropped(t *testing.T) {
	vault := t.TempDir()
	if err := os.WriteFile(Path(vault), []byte("- chekpoint: off\n- distil: maybe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Problems) != 2 {
		t.Errorf("want both bad lines reported, got %v", c.Problems)
	}
}

func TestAnAutoFileThatCannotBeReadRunsNothing(t *testing.T) {
	vault := t.TempDir()
	if err := os.WriteFile(Path(vault), []byte("auto: off\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(Path(vault)); err == nil {
		t.Skip("running as a user who can read a 0000 file")
	}
	for _, s := range Switches() {
		if On(vault, s) {
			t.Errorf("%s ran under an auto.md nobody could read", s)
		}
	}
}

// Load reads the last line for a switch, so a change that rewrote only the
// first would print the old state, exit 0, and change nothing.
func TestASwitchWrittenTwiceByHandIsChangedEverywhere(t *testing.T) {
	vault := t.TempDir()
	hand := "- dream: on\nauto: on\n\nlater, again:\n- dream: on\nauto: on\n"
	if err := os.WriteFile(Path(vault), []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Set(vault, "dream", false); err != nil {
		t.Fatal(err)
	}
	if err := SetMaster(vault, false); err != nil {
		t.Fatal(err)
	}
	c, err := Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	if c.On(Dream) || c.Master == nil || *c.Master {
		raw, _ := os.ReadFile(Path(vault))
		t.Errorf("auto config changed the first line and left the one that counts:\n%s", raw)
	}
}
