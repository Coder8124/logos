package main

import (
	"strings"
	"testing"
)

func TestAnEmptyAutoMemoryImportSaysMemoriesNotMemorys(t *testing.T) {
	standInAutoMemory(t, nil)
	out := importRules(t, claudeAutoMemorySource)
	if !strings.Contains(out, "no claude-auto-memory memories in") {
		t.Errorf("the empty import does not say memories:\n%s", out)
	}
}

// A Finder .DS_Store is in every folder a Mac user opened. Reported on every
// run, it taught people to read past the Left out lines, which are the ones
// that say a real note was not imported.
func TestHiddenFilesInTheAutoMemoryFolderAreNotReportedAsLeftOut(t *testing.T) {
	standInAutoMemory(t, map[string]string{
		".DS_Store":         "\x00\x00\x00\x01Bud1",
		"project_freeze.md": projectNote,
		"scratch.txt":       "not a note",
		".draft.md":         "Ship the beta to ten users first.\n",
	})
	out := importRules(t, claudeAutoMemorySource)
	if got := pendingFrom(t, claudeAutoMemorySource); len(got) != 2 {
		t.Errorf("queued %q, want the note and the hidden .md note", texts(got))
	}
	if strings.Contains(out, ".DS_Store") {
		t.Errorf("a hidden file was reported:\n%s", out)
	}
	if !strings.Contains(out, "scratch.txt: not a note") {
		t.Errorf("a visible file that is not a note is no longer reported:\n%s", out)
	}
}

// Auto memory is one note per file, so a line per file is a line per memory:
// forty notes were forty rows saying "1 memory".
func TestAutoMemoryIsFoundAsOneLinePerFolderAndScope(t *testing.T) {
	user := "---\nname: who\ndescription: who the user is\nmetadata:\n  type: user\n---\n\nThe user is a solo developer.\n"
	standInAutoMemory(t, map[string]string{
		"feedback_trailer.md": feedbackNote,
		"project_freeze.md":   projectNote,
		"project_other.md":    strings.Replace(projectNote, "2026-11-05", "2026-12-01", 1),
		"user_who.md":         user,
	})
	out := importRules(t, claudeAutoMemorySource)
	start := strings.Index(out, "Found:")
	if start < 0 {
		t.Fatalf("no Found list:\n%s", out)
	}
	found, _, _ := strings.Cut(out[start:], "\n\n")
	lines := strings.Split(strings.TrimSpace(found), "\n")[1:]
	if len(lines) != 2 {
		t.Fatalf("want one line for the project's notes and one for the user's, got:\n%s", found)
	}
	if !strings.Contains(found, "3 memories, for project shop") || !strings.Contains(found, "1 memory, for every project") {
		t.Errorf("the folder lines do not count by scope:\n%s", found)
	}
	if strings.Contains(found, ".md") {
		t.Errorf("a note file is named in the Found list:\n%s", found)
	}
}
