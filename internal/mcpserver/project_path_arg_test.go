package mcpserver

import "testing"

// A model asked "which project" answers with the directory the host handed it.
// That argument was filed verbatim, so a checkpoint landed at
// sessions/users/x/ideaprojects/brain/ — four levels deep, where session.Scopes
// reads exactly two. The checkpoint existed and nothing could ever see it
// again: not list_projects, not resume by name, not any count or dead-end
// check.
func TestAnAbsolutePathAsTheProjectIsFiledUnderTheProjectItNames(t *testing.T) {
	if got := normalizeProjectArg("/Users/pragun/IdeaProjects/brain"); got != "brain" {
		t.Errorf("want brain, got %q — the checkpoint would be four levels deep and unreachable", got)
	}
	if got := normalizeProjectArg("/tmp/abs-escape"); got != "abs-escape" {
		t.Errorf("want abs-escape, got %q", got)
	}
	if got := normalizeProjectArg("~/code/kestrel"); got != "kestrel" {
		t.Errorf("want kestrel, got %q", got)
	}
	if got := normalizeProjectArg("../escape"); got != "escape" {
		t.Errorf("want escape, got %q", got)
	}
}

// The same argument reaching remember wrote a machine-specific path carrying
// the user's account name into memories/fact.md — a file this product tells
// people to keep in git — and left the fact unreachable by the only name a
// human would type.
func TestAMemoryIsNotFiledUnderAnAbsolutePathCarryingTheUsersAccountName(t *testing.T) {
	sess := &Session{Server: &Server{DB: testDB(t)}}
	if got := sess.resolveProject("/Users/pragun/IdeaProjects/brain"); got != "brain" {
		t.Errorf("remember would store project %q, putting a home directory in the vault", got)
	}
}

// One separator is the qualified scope resolveContinuity documents — a project
// and the linked worktree it is being worked on in. Reducing that to a
// basename would file every worktree as a project of its own, undoing #140.
func TestAQualifiedWorktreeScopeIsLeftAlone(t *testing.T) {
	if got := normalizeProjectArg("kestrel/feature-a"); got != "kestrel/feature-a" {
		t.Errorf("a worktree scope was reduced to %q", got)
	}
}

// A plain name is the overwhelmingly common case and must cost nothing.
func TestAPlainProjectNameIsUnchanged(t *testing.T) {
	for _, name := range []string{"brain", "kestrel-one", "Heron"} {
		if got := normalizeProjectArg(name); got != name {
			t.Errorf("%q became %q", name, got)
		}
	}
}
