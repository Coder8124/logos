package main

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// screenPlays stands in for the terminal across several menus: each call
// answers the next menu with its row, how it was left, and the rows to tick.
type screenPlay struct {
	sel   int
	res   pick
	ticks []int
}

func playMenus(t *testing.T, plays ...screenPlay) *[]menu {
	t.Helper()
	var shown []menu
	was := pickOnTerminal
	pickOnTerminal = func(m menu) (int, pick, int, error) {
		if len(shown) == len(plays) {
			t.Fatalf("an unexpected menu opened: %q", m.title)
		}
		p := plays[len(shown)]
		shown = append(shown, m)
		for _, r := range p.ticks {
			m.checked[r] = true
		}
		return p.sel, p.res, len(m.rows) + 1, nil
	}
	t.Cleanup(func() { pickOnTerminal = was })
	return &shown
}

// recorder is a runOne that writes down each command line instead of running
// it, failing the ones named in fail.
func recorder(fail ...string) (*[]string, func(string, []string) error) {
	var ran []string
	return &ran, func(cmd string, args []string) error {
		line := strings.TrimSpace(cmd + " " + strings.Join(args, " "))
		ran = append(ran, line)
		if slices.Contains(fail, line) {
			return errors.New("boom")
		}
		return nil
	}
}

func rowOf(t *testing.T, cmd, flags string) int {
	t.Helper()
	for i, r := range flagMenus[cmd] {
		if r.flags == flags {
			return i
		}
	}
	t.Fatalf("logos %s has no %q row", cmd, flags)
	return -1
}

func TestEnterWithNothingTickedRunsTheHighlightedFormAlone(t *testing.T) {
	rows := flagMenus["show"]
	if got := flagRuns(rows, make([]bool, len(rows)), 0); !reflect.DeepEqual(got, [][]string{{}}) {
		t.Errorf("Enter on the first row ran %v, want the bare command", got)
	}
	got := flagRuns(rows, make([]bool, len(rows)), rowOf(t, "show", "--plans"))
	if !reflect.DeepEqual(got, [][]string{{"--plans"}}) {
		t.Errorf("Enter on --plans ran %v", got)
	}
}

func TestAModifierTickedWithoutItsModeRunsUnderIt(t *testing.T) {
	rows := flagMenus["show"]
	checked := make([]bool, len(rows))
	checked[rowOf(t, "show", "--list")] = true
	if got := flagRuns(rows, checked, 0); !reflect.DeepEqual(got, [][]string{{"--graph", "--list"}}) {
		t.Errorf("ticking --list alone ran %v, want show --graph --list", got)
	}
}

func TestTickedModesRunOnceEachInMenuOrderWithTheirOwnModifiers(t *testing.T) {
	rows := flagMenus["doctor"]
	checked := make([]bool, len(rows))
	for _, f := range []string{"--egress", "--probe", "--verbose"} {
		checked[rowOf(t, "doctor", f)] = true
	}
	want := [][]string{{"--verbose", "--probe"}, {"--egress"}}
	if got := flagRuns(rows, checked, 0); !reflect.DeepEqual(got, want) {
		t.Errorf("ran %v, want %v — --probe and --verbose belong to the bare doctor, not to --egress", got, want)
	}
}

func TestTheFlagMenuRunsEveryChosenFormAndNamesTheOnesThatFailed(t *testing.T) {
	playMenus(t, screenPlay{0, picked, []int{rowOf(t, "stats", "--week"), rowOf(t, "stats", "--memory"), 0}})
	ran, runOne := recorder("stats --usage --week")
	err := runFlagMenu("stats", flagMenus["stats"], runOne)
	if want := []string{"stats", "stats --usage --week", "stats --memory"}; !reflect.DeepEqual(*ran, want) {
		t.Errorf("ran %v, want %v — one failure must not stop the rest", *ran, want)
	}
	if err == nil || !strings.Contains(err.Error(), "1 of 3 failed") || !strings.Contains(err.Error(), "logos stats --usage --week") {
		t.Errorf("the failure is not named in the exit error: %v", err)
	}
}

func TestACancelledFlagMenuRunsNothing(t *testing.T) {
	playMenus(t, screenPlay{2, cancelled, nil})
	ran, runOne := recorder()
	if err := runFlagMenu("review", flagMenus["review"], runOne); err != nil || len(*ran) != 0 {
		t.Errorf("a cancel ran %v, err %v", *ran, err)
	}
}

// A hook or a script runs a bare `logos show` too, and a menu there waits on
// a keypress nobody can send.
func TestABareCommandWithNoTerminalRunsAsTypedWithoutAMenu(t *testing.T) {
	atTerminal(t, false)
	playMenus(t) // any menu fails the test
	ran, runOne := recorder()
	for _, cmd := range []string{"show", "stats", "review", "doctor", "watch", "config"} {
		if err := runTyped(cmd, nil, runOne); err != nil {
			t.Fatal(err)
		}
	}
	if len(*ran) != 6 {
		t.Errorf("ran %v", *ran)
	}
}

func TestAnythingTypedAfterTheCommandSkipsTheMenu(t *testing.T) {
	atTerminal(t, true)
	playMenus(t)
	ran, runOne := recorder()
	if err := runTyped("show", []string{"brain"}, runOne); err != nil || !reflect.DeepEqual(*ran, []string{"show brain"}) {
		t.Errorf("ran %v, err %v", *ran, err)
	}
}

func TestABareFlagCommandAtATerminalOpensItsMenu(t *testing.T) {
	atTerminal(t, true)
	shown := playMenus(t, screenPlay{0, picked, nil})
	ran, runOne := recorder()
	if err := runTyped("doctor", nil, runOne); err != nil {
		t.Fatal(err)
	}
	if len(*shown) != 1 || !reflect.DeepEqual(*ran, []string{"doctor"}) {
		t.Errorf("menus %d, ran %v — Enter alone must still be plain doctor", len(*shown), *ran)
	}
}

// The menus are a second copy of what the commands accept, so they can drift:
// a flag renamed in its command would still be offered here and refused when
// run. Every form must pass the same flag check a typed line does, and be
// documented where a person would look it up.
func TestEveryFlagAMenuOffersIsOneItsCommandAcceptsAndDocuments(t *testing.T) {
	var help strings.Builder
	helpAll(&help)
	documented := help.String() + reviewUsage
	for cmd, rows := range flagMenus {
		if rows[0].flags != "" {
			t.Errorf("logos %s: the first row must be the bare command, so Enter alone changes nothing", cmd)
		}
		for _, r := range rows {
			for _, f := range strings.Fields(r.flags) {
				if !strings.Contains(documented, f) {
					t.Errorf("logos %s offers %s, which no help text mentions", cmd, f)
				}
			}
			if !r.isMode() && !slices.ContainsFunc(rows, func(m flagRow) bool { return m.isMode() && m.flags == r.mode }) {
				t.Errorf("logos %s %s modifies %q, which is not a row", cmd, r.flags, r.mode)
			}
		}
		all := make([]bool, len(rows))
		for i := range all {
			all[i] = true
		}
		for _, args := range flagRuns(rows, all, 0) {
			verb, rest, _ := surface(cmd, args)
			if err := checkCommandFlags(verb, rest); err != nil {
				t.Errorf("logos %s %v is refused when run: %v", cmd, args, err)
			}
		}
	}
}

func TestTheConfigMenuSetsTheValuePickedAndEscGoesBackToTheSettings(t *testing.T) {
	think := 0
	for i, s := range configMenu {
		if s.setting == "think" {
			think = i + 1
		}
	}
	shown := playMenus(t,
		screenPlay{think, picked, nil},
		screenPlay{0, wentBack, nil},
		screenPlay{think, picked, nil},
		screenPlay{3, picked, nil}, // medium
	)
	ran, runOne := recorder()
	if err := runConfigMenu(runOne); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*ran, []string{"config think medium"}) {
		t.Errorf("ran %v", *ran)
	}
	if back := (*shown)[2]; back.sel != think || back.over == 0 {
		t.Errorf("going back reopened the settings on row %d over %d lines; want row %d drawn over the old frames", back.sel, back.over, think)
	}
}

func TestEnterEnterInTheConfigMenuOnlyLooks(t *testing.T) {
	ran, runOne := recorder()
	playMenus(t, screenPlay{0, picked, nil})
	if err := runConfigMenu(runOne); err != nil || !reflect.DeepEqual(*ran, []string{"config"}) {
		t.Errorf("Enter on the first row ran %v, err %v; want the list of settings", *ran, err)
	}
	// Every setting whose bare form only reports opens on that report. deny is
	// the one whose bare form acts, and its rows must both say so.
	for i, s := range configMenu {
		if !slices.Contains(configSettings, s.setting) {
			t.Errorf("the config menu offers %q, which is not a setting", s.setting)
		}
		if len(s.choices) == 0 {
			continue
		}
		*ran = nil
		playMenus(t, screenPlay{i + 1, picked, nil}, screenPlay{0, picked, nil})
		if err := runConfigMenu(runOne); err != nil {
			t.Fatal(err)
		}
		if s.setting == "deny" {
			if !strings.Contains(s.choices[0].label, "writes") {
				t.Errorf("deny's first row does not say it writes the marker: %q", s.choices[0].label)
			}
			continue
		}
		if !reflect.DeepEqual(*ran, []string{"config " + s.setting}) || !strings.HasPrefix(s.choices[0].label, "show") {
			t.Errorf("Enter, Enter on %s ran %v under %q; want only a look", s.setting, *ran, s.choices[0].label)
		}
	}
}
