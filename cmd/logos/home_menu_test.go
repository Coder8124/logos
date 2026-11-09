package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func homeRowOf(t *testing.T, cmd string) int {
	t.Helper()
	for i, r := range homeRows {
		if r.cmd == cmd {
			return i
		}
	}
	t.Fatalf("the home menu has no %s row", cmd)
	return -1
}

func TestTheHomeMenuOpensOnResumeSoEnterAloneResumes(t *testing.T) {
	if homeRows[0].cmd != "resume" {
		t.Fatalf("the first home row is %q, want resume", homeRows[0].cmd)
	}
	atTerminal(t, true)
	shown := playMenus(t, screenPlay{sel: 0, res: picked})
	ran, runOne := recorder()
	if err := runHomeMenu(runOne); err != nil {
		t.Fatal(err)
	}
	if (*shown)[0].sel != 0 || !reflect.DeepEqual(*ran, []string{"resume"}) {
		t.Fatalf("sel %d ran %v, want resume", (*shown)[0].sel, *ran)
	}
}

func TestACommandPickedFromTheHomeMenuOpensItsOwnMenuNext(t *testing.T) {
	atTerminal(t, true)
	shown := playMenus(t,
		screenPlay{sel: homeRowOf(t, "stats"), res: picked},
		screenPlay{sel: 0, res: picked},
	)
	ran, runOne := recorder()
	if err := runHomeMenu(runOne); err != nil {
		t.Fatal(err)
	}
	if len(*shown) != 2 || !strings.Contains((*shown)[1].title, "logos stats") {
		t.Fatalf("menus shown %d, want the stats menu second", len(*shown))
	}
	if !reflect.DeepEqual(*ran, []string{"stats"}) {
		t.Fatalf("ran %v, want stats", *ran)
	}
}

func TestEveryHomeRowIsACommandThatRunsBare(t *testing.T) {
	// search needs a query, which its row's text box supplies; every other
	// row runs with nothing after it, so none can open on a usage error.
	for _, r := range homeRows {
		if r.cmd == "search" {
			continue
		}
		if strings.Contains(r.label, "<") {
			t.Fatalf("%s row asks for an argument: %q", r.cmd, r.label)
		}
	}
}

func TestTheSearchRowRunsSearchOnWhatWasTyped(t *testing.T) {
	atTerminal(t, true)
	was := pickOnTerminal
	pickOnTerminal = func(m menu) (int, pick, int, error) {
		m.field.value = "flaky timeout --answer"
		return m.field.row, picked, 1, nil
	}
	t.Cleanup(func() { pickOnTerminal = was })
	ran, runOne := recorder()
	if err := runHomeMenu(runOne); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*ran, []string{"search flaky timeout --answer"}) {
		t.Fatalf("ran %v", *ran)
	}
}

func TestCancellingTheHomeMenuRunsNothing(t *testing.T) {
	atTerminal(t, true)
	playMenus(t, screenPlay{res: cancelled})
	ran, runOne := recorder()
	if err := runHomeMenu(runOne); err != nil {
		t.Fatal(err)
	}
	if len(*ran) != 0 {
		t.Fatalf("ran %v after a cancel", *ran)
	}
}

// The search row is a text box in a menu that is not a checklist. Typing into
// it and pressing Enter must end the menu on that row with the text: before,
// an answer could only tick a checklist row, and with no checklist it wrote
// into a nil slice and crashed.
func TestEnterInATextBoxOfAPlainMenuRunsThatRowWithTheText(t *testing.T) {
	f := &menuField{row: 1, prompt: "search for: ", check: func(s string) (string, error) { return strings.TrimSpace(s), nil }}
	var screen bytes.Buffer
	sel, res, _ := runPicker(typedSlowly("j\rflaky test\r"), &screen, menu{title: "t", hint: "h", rows: []string{"a", "search", "c"}, field: f}, 80)
	if res != picked || sel != 1 || f.value != "flaky test" {
		t.Fatalf("sel %d res %v value %q, want row 1 picked with the text", sel, res, f.value)
	}
}

func TestAnEmptySearchKeepsTheTextBoxOpen(t *testing.T) {
	f := &menuField{row: 0, prompt: "search for: ", check: func(s string) (string, error) {
		if strings.TrimSpace(s) == "" {
			return "", errEmptyQueryForTest
		}
		return s, nil
	}}
	var screen bytes.Buffer
	_, res, _ := runPicker(typedSlowly("\r\rq"), &screen, menu{title: "t", hint: "h", rows: []string{"search"}, field: f}, 80)
	if res == picked {
		t.Fatal("an empty query ran a search")
	}
	if !strings.Contains(screen.String(), errEmptyQueryForTest.Error()) {
		t.Fatalf("the reason was not shown:\n%q", screen.String())
	}
}

var errEmptyQueryForTest = errors.New("type something to search for")

// The wheel moved the selection while a text box was being typed into, and
// Enter then ran whichever row the wheel had reached with the typed text —
// a query typed into search could start an index rebuild.
func TestScrollingWhileTypingInTheSearchBoxStillRunsSearch(t *testing.T) {
	f := &menuField{row: 1, prompt: "search for: ", check: func(s string) (string, error) { return strings.TrimSpace(s), nil }}
	var screen bytes.Buffer
	keys := "j\rflaky" + "\x1b[<65;5;18M\x1b[<65;5;18M" + "\r"
	sel, res, _ := runPicker(keysOf(keys), &screen, menu{title: "t", hint: "h", rows: []string{"a", "search", "c", "d"}, field: f, mouse: true}, 80)
	if res != picked || sel != 1 || f.value != "flaky" {
		t.Fatalf("sel %d res %v value %q, want the search row picked with the text", sel, res, f.value)
	}
}
