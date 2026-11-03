package main

import (
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

// A note's kind came from metadata.type first while the every-project check
// took either field, so `type: user` beside `metadata: {type: project}` was
// imported as Context and scoped to every project: one repository's context
// recalled in every other. Both now follow the one type the kind came from.
func TestANoteWhoseTwoTypeFieldsDisagreeIsScopedByTheTypeItsKindCameFrom(t *testing.T) {
	standInAutoMemory(t, map[string]string{
		"mixed.md": "---\nname: mixed\ntype: user\nmetadata:\n  type: project\n---\n\nThe staging deploy runs from the release branch.\n",
	})
	importRules(t, claudeAutoMemorySource)
	pending := pendingFrom(t, claudeAutoMemorySource)
	if len(pending) != 1 {
		t.Fatalf("queued %d notes, want 1", len(pending))
	}
	if pending[0].Kind != memory.Context || pending[0].Project == "" {
		t.Errorf("queued as %s for project %q, want context scoped to this project", pending[0].Kind, pending[0].Project)
	}
}

// An editor that saves a byte order mark put it before the opening fence, so
// the note read as having no frontmatter and its name: and description: lines
// were imported as the memory's text.
func TestANoteSavedWithAByteOrderMarkKeepsItsFrontmatterOutOfTheMemory(t *testing.T) {
	standInAutoMemory(t, map[string]string{
		"bom.md": "\ufeff" + projectNote,
	})
	importRules(t, claudeAutoMemorySource)
	got := texts(pendingFrom(t, claudeAutoMemorySource))
	if len(got) != 1 || got[0] != "Merges freeze on 2026-11-05 for the launch." {
		t.Errorf("queued %q, want only the note's body", got)
	}
}
