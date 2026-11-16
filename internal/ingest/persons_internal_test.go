package ingest

import "testing"

// PersonsText is PersonWrote's answer in full, for a reader that counts what
// the person wrote rather than asking whether they wrote anything.
func TestPersonsTextIsWhatThePersonWroteWithTheHostsTextTakenOut(t *testing.T) {
	for prompt, want := range map[string]string{
		"fix the checkout crash": "fix the checkout crash",
		"fix the crash\n\n<system-reminder>hooks ran</system-reminder>\nin the empty cart": "fix the crash in the empty cart",
		"<command-name>/clear</command-name>":                                              "",
		"/compact":                                                                         "",
		"<local-command-stdout>ok</local-command-stdout>":                                  "",
		"see this <pasted_content id=\"3\">two hundred lines</pasted_content> and fix it":  "see this and fix it",
		"no": "no",
		// What the person ran at the shell with "!", and what it printed.
		"<bash-input>logos usage</bash-input>":                                  "",
		"<bash-stdout>12 packs sent</bash-stdout><bash-stderr></bash-stderr>":   "",
		"<task-notification>\n<status>completed</status>\n</task-notification>": "",
		"[Request interrupted by user for tool use]":                            "",
		"[Request interrupted by user]\nuse the staging database":               "use the staging database",
		"[Image #1] the dialog says the connector is on":                        "the dialog says the connector is on",
		"[Image #2]": "",
		"This session is being continued from a previous conversation that ran out of context.": "",
	} {
		if got := PersonsText(prompt); got != want {
			t.Errorf("PersonsText(%q) = %q, want %q", prompt, got, want)
		}
		if PersonWrote(prompt) != (want != "") {
			t.Errorf("PersonWrote(%q) disagrees with PersonsText", prompt)
		}
	}
}

func TestEditToolIsAFileToolThatChangesTheFile(t *testing.T) {
	for _, name := range []string{"Edit", "Write", "MultiEdit", "apply_patch", "search_replace", "edit_file", "edit_file_v2", "delete_file"} {
		if !EditTool(name) || !isFileTool(name) {
			t.Errorf("%s is an edit, and was not read as one", name)
		}
	}
	for _, name := range []string{"read_file", "read_file_v2"} {
		if EditTool(name) || !isFileTool(name) {
			t.Errorf("%s names a file for the harvest and changes nothing: a file tool, not an edit", name)
		}
	}
	if !ShellTool("exec_command") || !ShellTool("run_terminal_command_v2") || ShellTool("Edit") {
		t.Error("ShellTool does not agree with the harvest's own list")
	}
}

// A paste the host marks is kept out of what the person wrote and is still
// theirs to have shown: an error pasted back with no word beside it is a turn.
func TestPastedLinesIsWhatPersonsLinesLeftOut(t *testing.T) {
	prompt := "see this <pasted_content id=\"3\">Traceback (most recent call last):\n  File \"app.py\", line 3\n\nKeyError: 'id'</pasted_content> and fix it"
	got := PastedLines(prompt)
	want := []string{"Traceback (most recent call last):", "File \"app.py\", line 3", "KeyError: 'id'"}
	if len(got) != len(want) {
		t.Fatalf("PastedLines = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if PastedLines("fix the checkout crash") != nil {
		t.Error("a turn with no paste in it has no pasted lines")
	}
}
