package contextpack

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

// "One repository's facts do not surface in another" is a promise the README
// makes, and this is where a user meets it. The filter below the Surface call
// only admits a global preference, testing `m.Project == ""` — but every
// memory arrived with an empty project, because the query behind Surface never
// selected the column. So a preference stated while working on kestrel was
// presented in heron's pack as one of the user's standing preferences, with no
// label saying it came from somewhere else.
func TestAnotherProjectsPreferenceDoesNotAppearInThisProjectsPack(t *testing.T) {
	ix := seedVault(t)
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	elsewhere := memory.Memory{
		Text:    "The user prefers tabs over spaces in Go files",
		Kind:    memory.Preference,
		Project: "some-other-repo",
		Source:  "test",
	}
	if _, err := memory.Store(ix.DB, nil, "", &elsewhere); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range p.Preferences {
		if strings.Contains(m.Text, "tabs over spaces") {
			t.Errorf("a preference filed under some-other-repo is in kestrel-one's pack as a standing preference")
		}
	}
	if strings.Contains(p.Render(), "tabs over spaces") {
		t.Errorf("another project's preference is rendered into this project's pack:\n%s", p.Render())
	}
}

// The global case is the one the filter exists to let through: a preference
// with no project is about the user, not about a repository, and belongs in
// every pack.
func TestAGlobalPreferenceStillAppearsInAProjectsPack(t *testing.T) {
	ix := seedVault(t)
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	global := memory.Memory{
		Text:   "The user prefers written proposals over meetings",
		Kind:   memory.Preference,
		Source: "test",
	}
	if _, err := memory.Store(ix.DB, nil, "", &global); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range p.Preferences {
		if strings.Contains(m.Text, "written proposals") {
			return
		}
	}
	t.Error("a global preference was dropped from a project's pack")
}
