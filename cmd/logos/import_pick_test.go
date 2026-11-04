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

func TestEnterWithNothingTickedImportsTheHighlightedSource(t *testing.T) {
	ticks(t, 5, picked)
	chosen, ok, err := chooseImportSources()
	if err != nil || !ok || len(chosen) != 1 || chosen[0].from != "git" {
		t.Fatalf("chose %v ok=%v err=%v, want only git", fromOf(chosen), ok, err)
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
