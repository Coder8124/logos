package contextpack

import (
	"testing"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/project"
)

// Every other arm of a pack is scoped to the project being asked about. The
// notes arm passed no project at all, so "From the vault" ranked the whole
// vault by similarity to the task and handed one project's prose to another.
func TestAnotherProjectsNoteIsNotInThisProjectsVaultSection(t *testing.T) {
	p := Pack{Project: &project.Project{Slug: "projects/kestrel-one", Name: "kestrel-one"}}

	got := p.withoutForeignProjects([]index.Hit{
		{Slug: "projects/heron", Kind: "project", Title: "Heron"},
		{Slug: "projects/heron/deploys", Kind: "note", Title: "Heron deploys"},
		{Slug: "projects/kestrel-one", Kind: "project", Title: "Kestrel One"},
	})
	for _, h := range got {
		if h.Slug == "projects/heron" || h.Slug == "projects/heron/deploys" {
			t.Errorf("heron's prose is in kestrel-one's pack: %q", h.Slug)
		}
	}
	if len(got) != 1 {
		t.Errorf("want only kestrel-one's own note, got %v", got)
	}
}

// Shared prose belongs to no single project and is the material the vault
// exists to accumulate. Scoping that out would answer the cross-project leak
// by making the section empty, which is the same loss in the other direction.
func TestASharedTopicStillReachesAProjectsVaultSection(t *testing.T) {
	p := Pack{Project: &project.Project{Slug: "projects/kestrel-one", Name: "kestrel-one"}}

	got := p.withoutForeignProjects([]index.Hit{
		{Slug: "topics/bom-cost", Kind: "topic"},
		{Slug: "decisions/no-second-mic", Kind: "decision"},
	})
	if len(got) != 2 {
		t.Errorf("shared vault prose was dropped as though it belonged to another project: %v", got)
	}
}

// A pack with no project resolved is asking about the whole vault, and has to
// still be answered from it — otherwise the filter turns "no project" into
// "no notes".
func TestAnUnscopedPackStillSeesEveryProjectsNotes(t *testing.T) {
	var p Pack

	hits := []index.Hit{
		{Slug: "projects/heron", Kind: "project"},
		{Slug: "projects/kestrel-one", Kind: "project"},
	}
	if got := p.withoutForeignProjects(hits); len(got) != 2 {
		t.Errorf("an unscoped pack lost the notes it was asking for: %v", got)
	}
}
