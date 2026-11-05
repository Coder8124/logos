package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

// ticks stands in for the terminal: it ticks the given rows of the import
// menu, leaves the cursor on sel, and ends the menu with res.
func ticks(t *testing.T, sel int, res pick, rows ...int) *int {
	t.Helper()
	shown := 0
	was := pickOnTerminal
	pickOnTerminal = func(m menu) (int, pick, int, error) {
		shown++
		if m.checked == nil {
			t.Fatalf("the import menu %q is not a checklist", m.title)
		}
		for _, r := range rows {
			m.checked[r] = true
		}
		return sel, res, len(m.rows) + 1, nil
	}
	t.Cleanup(func() { pickOnTerminal = was })
	return &shown
}

func atTerminal(t *testing.T, yes bool) {
	t.Helper()
	was := interactive
	interactive = func() bool { return yes }
	t.Cleanup(func() { interactive = was })
}

func fromOf(sources []importSource) []string {
	var out []string
	for _, s := range sources {
		out = append(out, s.from)
	}
	return out
}

func TestSpaceTicksAChecklistRowAndEnterLeavesTheTicks(t *testing.T) {
	checked := make([]bool, 3)
	var screen bytes.Buffer
	// A byte per read, as typed: a batch of keys redraws once, at its end.
	typed := bufio.NewReader(iotest.OneByteReader(strings.NewReader(" jj  j \r")))
	i, res, _ := runPicker(typed, &screen, menu{title: "t", hint: "h", rows: []string{"a", "b", "c"}, checked: checked}, 80)
	if res != picked || i != 2 {
		t.Fatalf("got row %d, %v; want row 2 picked", i, res)
	}
	if want := []bool{true, false, true}; checked[0] != want[0] || checked[1] != want[1] || checked[2] != want[2] {
		t.Errorf("ticks %v, want %v", checked, want)
	}
	if !strings.Contains(screen.String(), "> [x] c") || !strings.Contains(screen.String(), "  [ ] b") {
		t.Errorf("the checklist does not draw its boxes:\n%q", screen.String())
	}
}

func TestSpaceDoesNothingInAMenuThatIsNotAChecklist(t *testing.T) {
	var screen bytes.Buffer
	if i, res, _ := runPicker(keysOf(" j\r"), &screen, menu{title: "t", hint: "h", rows: []string{"a", "b"}}, 80); res != picked || i != 1 {
		t.Fatalf("got row %d, %v; want row 1 picked", i, res)
	}
	if strings.Contains(screen.String(), "[ ]") {
		t.Errorf("a plain menu drew checkboxes:\n%q", screen.String())
	}
}

func TestTheImportMenuImportsEveryTickedSourceInOrder(t *testing.T) {
	ticks(t, 0, picked, 4, 1)
	chosen, ok, err := chooseImportSources()
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got := strings.Join(fromOf(chosen), " "); got != "claude-md transcripts" {
		t.Errorf("chose %q, want the two ticked, in menu order", got)
	}
}

func TestCancellingTheImportMenuImportsNothing(t *testing.T) {
	for _, res := range []pick{cancelled, wentBack} {
		ticks(t, 0, res, 0, 1)
		if chosen, ok, err := chooseImportSources(); err != nil || ok || chosen != nil {
			t.Errorf("after %v: chose %v ok=%v err=%v, want a cancel", res, fromOf(chosen), ok, err)
		}
	}
}

func TestABareImportWithoutATerminalStillPrintsUsage(t *testing.T) {
	atTerminal(t, false)
	shown := ticks(t, 0, picked, 0)
	err := runImport(nil)
	if err == nil || !strings.Contains(err.Error(), "usage: logos import") {
		t.Fatalf("got %v, want the usage error", err)
	}
	if *shown != 0 {
		t.Error("the menu opened without a terminal")
	}
}

// The session-start hook must never wait on a keypress, terminal or not.
func TestABareImportInsideAHookNeverOpensTheMenu(t *testing.T) {
	atTerminal(t, true)
	t.Setenv("LOGOS_HOOK", "1")
	shown := ticks(t, 0, picked, 0)
	if err := runImport(nil); err == nil || !strings.Contains(err.Error(), "usage: logos import") {
		t.Fatalf("got %v, want the usage error", err)
	}
	if *shown != 0 {
		t.Error("the menu opened inside a hook")
	}
}

func TestASourceThatFailsFromTheMenuIsNamedInTheExitError(t *testing.T) {
	atTerminal(t, true)
	t.Setenv("LOGOS_HOOK", "")
	t.Setenv("LOGOS_VAULT", filepath.Join(t.TempDir(), "no-vault-here"))
	ticks(t, 0, picked, 4)
	err := runImport(nil)
	if err == nil || !strings.Contains(err.Error(), "1 of 1 sources failed: transcripts") {
		t.Fatalf("got %v, want the failed source named", err)
	}
}

// A menu and the prompt after it read one stream. The Enter that ends the
// menu and the answer typed after it can arrive in one read; a prompt reading
// stdin on its own would find the answer gone into the menu's buffer.
func TestAnAnswerTypedRightAfterTheMenuReachesThePrompt(t *testing.T) {
	wasKb, wasFrom, wasAns, wasAnsFrom := keyboard, keyboardFrom, answers, answersFrom
	t.Cleanup(func() { keyboard, keyboardFrom, answers, answersFrom = wasKb, wasFrom, wasAns, wasAnsFrom })
	keyboard, keyboardFrom, answers = keysOf("\ry\n"), os.Stdin, nil

	var screen bytes.Buffer
	if _, res, _ := runPicker(keyboard, &screen, menu{title: "t", hint: "h", rows: []string{"a"}, checked: []bool{false}}, 80); res != picked {
		t.Fatalf("menu ended %v, want picked", res)
	}
	if !confirmNo("Import these?") {
		t.Error("the y typed after the menu's Enter did not reach the prompt")
	}
}

// Ticking transcripts in the menu is how most people meet ingest's one-time
// consent question, so it reads the menu's stream like every other prompt.
func TestIngestConsentTypedRightAfterTheMenuIsGranted(t *testing.T) {
	wasKb, wasFrom, wasAns, wasAnsFrom := keyboard, keyboardFrom, answers, answersFrom
	t.Cleanup(func() { keyboard, keyboardFrom, answers, answersFrom = wasKb, wasFrom, wasAns, wasAnsFrom })
	keyboard, keyboardFrom, answers = keysOf("\ry\n"), os.Stdin, nil

	var screen bytes.Buffer
	if _, res, _ := runPicker(keyboard, &screen, menu{title: "t", hint: "h", rows: []string{"a"}, checked: []bool{false}}, 80); res != picked {
		t.Fatalf("menu ended %v, want picked", res)
	}
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(ingestConsentPath(vault)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureIngestConsent(vault, false); err != nil {
		t.Fatalf("the y typed after the menu's Enter did not grant consent: %v", err)
	}
}

// typedSlowly delivers keys a byte per read, as a person types them, so every
// key draws its own frame.
func typedSlowly(s string) *bufio.Reader {
	return bufio.NewReader(iotest.OneByteReader(strings.NewReader(s)))
}

// importMenu is the shape of the import menu, small: two rows, the second a
// folder field, and the row that starts the import.
func importMenu(checked []bool, f *menuField) menu {
	return menu{title: "t", hint: "h", rows: []string{"a", "folder"}, checked: checked, submit: "Import", field: f}
}

func TestEnterTicksARowAndOnlyTheLastRowStartsTheImport(t *testing.T) {
	checked := make([]bool, 3)
	var screen bytes.Buffer
	m := menu{title: "t", hint: "h", rows: []string{"a", "b", "c"}, checked: checked, submit: "Import"}
	if _, res, _ := runPicker(keysOf("\rjj\rk\r\rjj\r"), &screen, m, 80); res != picked {
		t.Fatalf("menu ended %v, want picked", res)
	}
	if want := []bool{true, false, true}; checked[0] != want[0] || checked[1] != want[1] || checked[2] != want[2] {
		t.Errorf("ticks %v, want %v: Enter on a row ticks and unticks it", checked, want)
	}
}

func TestTheLastRowRefusesToStartWithNothingTicked(t *testing.T) {
	checked := make([]bool, 1)
	var screen bytes.Buffer
	m := menu{title: "t", hint: "h", rows: []string{"a"}, checked: checked, submit: "Import"}
	// Input ends after the refused Enter: had it been taken, the menu would
	// have ended picked rather than with nobody left to choose.
	if _, res, _ := runPicker(keysOf("j\r"), &screen, m, 80); res != cancelled {
		t.Fatalf("menu ended %v, want it still open when the input ran out", res)
	}
	if !strings.Contains(screen.String(), "Tick at least one row first.") {
		t.Errorf("the refusal is not said:\n%q", screen.String())
	}
}

func TestAClickOnARowTicksItAndAClickOnTheLastRowImports(t *testing.T) {
	checked := make([]bool, 2)
	var screen bytes.Buffer
	m := menu{title: "t", hint: "h", rows: []string{"a", "b"}, checked: checked, submit: "Import", mouse: true}
	// The first frame is four lines and leaves the cursor on row 20, so its
	// title is on row 16, "b" on 18 and the last row on 19. A release (m)
	// follows each press and must not count as a second click.
	keys := "\x1b[20;1R" + "\x1b[<0;5;18M\x1b[<0;5;18m" + "\x1b[<0;5;16M" + "\x1b[<0;5;19M\x1b[<0;5;19m"
	if _, res, _ := runPicker(keysOf(keys), &screen, m, 80); res != picked {
		t.Fatalf("menu ended %v, want picked by the click on the last row", res)
	}
	if checked[0] || !checked[1] {
		t.Errorf("ticks %v, want only the clicked row", checked)
	}
	if !strings.Contains(screen.String(), "\x1b[6n") {
		t.Error("the menu never asked where it was drawn, so it cannot place a click")
	}
}

func TestAMenuWithoutTheMouseDoesNotAskWhereItIsDrawn(t *testing.T) {
	var screen bytes.Buffer
	runPicker(keysOf("\r"), &screen, menu{title: "t", hint: "h", rows: []string{"a"}}, 80)
	if strings.Contains(screen.String(), "\x1b[6n") {
		t.Error("a menu without mouse asked for the cursor, and its answer would reach the next prompt")
	}
}

func TestTheFolderRowTakesATypedFolderAndTicksIt(t *testing.T) {
	dir := t.TempDir()
	checked := make([]bool, 2)
	f := &menuField{row: 1, prompt: "Folder: ", check: importFolder}
	var screen bytes.Buffer
	// q and j are typed into the field, not taken as keys, and Backspace
	// takes them out again.
	keys := "j\r" + dir + "qj\x7f\x7f\r" + "j\r"
	if _, res, _ := runPicker(typedSlowly(keys), &screen, importMenu(checked, f), 300); res != picked {
		t.Fatalf("menu ended %v, want picked", res)
	}
	if f.value != dir || !checked[1] {
		t.Errorf("field %q ticked %v, want %q ticked", f.value, checked[1], dir)
	}
	if !strings.Contains(screen.String(), "folder: "+dir) {
		t.Errorf("the row does not show the folder it will read:\n%q", screen.String())
	}
}

func TestAFolderThatDoesNotExistKeepsTheFieldOpenAndSaysSo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	checked := make([]bool, 2)
	f := &menuField{row: 1, prompt: "Folder: ", check: importFolder}
	var screen bytes.Buffer
	// The j after the refused Enter is typed into the field, which is how
	// it shows the field stayed open.
	if _, res, _ := runPicker(typedSlowly("j\r"+missing+"\rj"), &screen, importMenu(checked, f), 80); res != cancelled {
		t.Fatalf("menu ended %v, want it still open", res)
	}
	if f.value != "" || checked[1] {
		t.Errorf("a missing folder was taken: %q ticked %v", f.value, checked[1])
	}
	// At 80 columns the path is longer than the line: the field shows its
	// end, where the typing is, and the whole refusal fits under it.
	if !strings.Contains(screen.String(), "  no folder by that name — fix it, or Esc to leave the field\r\n") || !strings.Contains(screen.String(), "/nopej█\r\n") {
		t.Errorf("the refusal or the open field's end is not on screen:\n%q", screen.String())
	}
}

func TestEscLeavesTheFolderFieldWithoutLeavingTheMenu(t *testing.T) {
	checked := make([]bool, 2)
	f := &menuField{row: 1, prompt: "Folder: ", check: importFolder}
	var screen bytes.Buffer
	if _, res, _ := runPicker(keysOf("j\rabc\x1b"), &screen, importMenu(checked, f), 80); res != cancelled {
		t.Fatalf("menu ended %v, want it open until the input ran out", res)
	}
	if f.value != "" || checked[1] {
		t.Errorf("Esc kept what was typed: %q ticked %v", f.value, checked[1])
	}
}

func TestAFolderInTheImportMenuRunsEachRulesSourceOnIt(t *testing.T) {
	dir := t.TempDir()
	was := pickOnTerminal
	pickOnTerminal = func(m menu) (int, pick, int, error) {
		m.checked[0] = true
		m.field.value, m.checked[m.field.row] = dir, true
		return len(m.rows), picked, 0, nil
	}
	t.Cleanup(func() { pickOnTerminal = was })
	chosen, ok, err := chooseImportSources()
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got := strings.Join(fromOf(chosen), " "); got != "claude-mem claude-md cursor-rules claude-auto-memory" {
		t.Fatalf("chose %q, want claude-mem then the three rules sources", got)
	}
	if chosen[0].dir != "" {
		t.Errorf("claude-mem was given the folder %q, which it was not ticked for", chosen[0].dir)
	}
	for _, s := range chosen[1:] {
		if s.dir != dir {
			t.Errorf("%s reads %q, want the folder %q", s.from, s.dir, dir)
		}
	}
}

func TestAFolderDraggedIntoTheTerminalIsTakenAsTyped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my notes")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, typed := range []string{dir, "'" + dir + "'", `"` + dir + `"`, strings.ReplaceAll(dir, " ", `\ `), "  " + dir + "  "} {
		if got, err := importFolder(typed); err != nil || got != dir {
			t.Errorf("%q gave %q, %v; want %q", typed, got, err, dir)
		}
	}
	file := filepath.Join(dir, "f.md")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := importFolder(file); err == nil || !strings.Contains(err.Error(), "not a folder") {
		t.Errorf("a file was taken as a folder: %v", err)
	}
	if got, err := importFolder("   "); err != nil || got != "" {
		t.Errorf("an empty field gave %q, %v; want it cleared", got, err)
	}
}
