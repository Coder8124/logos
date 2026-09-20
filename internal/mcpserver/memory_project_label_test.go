package mcpserver

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

func seedMemory(t *testing.T, db *sql.DB, text, project string) memory.Memory {
	t.Helper()
	m := memory.Memory{Text: text, Kind: memory.Fact, Project: project, Source: "test"}
	if _, err := memory.Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// recall has labelled foreign results since #155, for a reason its own comment
// gives: a memory from elsewhere must not read as this project's settled
// truth. list_memories renders one function away and never printed the
// project, so six memories from three projects arrived as one flat list —
// indistinguishable from six facts about the work in front of you.
func TestListMemoriesSaysWhichProjectEachMemoryCameFrom(t *testing.T) {
	db := testDB(t)
	seedMemory(t, db, "the staging port is 9090", "proj2")
	seedMemory(t, db, "kestrel deploys behind the corporate VPN at 10.2.0.7", "kestrel")
	srv := &Server{DB: db}

	out, err := srv.listMemories("proj2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "from kestrel") {
		t.Errorf("another project's memory is listed with nothing saying whose it is:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "staging port") && strings.Contains(line, "from") {
			t.Errorf("this project's own memory is labelled as foreign:\n%s", line)
		}
	}
}

// The documented use for list_memories is "before forgetting something", and
// forget takes the id printed here. An unlabelled id belonging to another
// repository is offered in the same list as this project's, so the obvious
// next call deletes work nobody in the session has opened.
func TestTheIdOfAForeignMemoryIsNotOfferedUnmarked(t *testing.T) {
	db := testDB(t)
	foreign := seedMemory(t, db, "the secret store is Vault, not SSM", "heron")
	srv := &Server{DB: db}

	out, err := srv.listMemories("proj2")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "secret store") {
			continue
		}
		if !strings.Contains(line, "heron") {
			t.Errorf("memory %d is offered for forgetting with no sign it belongs elsewhere:\n%s", foreign.ID, line)
		}
	}
}

// A global fact applies to every project, so it is never foreign and must not
// be labelled as though it belonged to someone else.
func TestAGlobalMemoryIsNotLabelledAsAnotherProjects(t *testing.T) {
	db := testDB(t)
	seedMemory(t, db, "the user prefers short replies", "")
	srv := &Server{DB: db}

	out, err := srv.listMemories("proj2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "from") {
		t.Errorf("a global fact is labelled as another project's:\n%s", out)
	}
}

// memory_diff renders every project's changes over the window in one list. An
// unlabelled line about another repository reads as a change to this one.
func TestMemoryDiffSaysWhichProjectEachChangeBelongsTo(t *testing.T) {
	db := testDB(t)
	seedMemory(t, db, "kestrel deploys behind the corporate VPN", "kestrel")
	seedMemory(t, db, "the staging port is 9090", "proj2")
	srv := &Server{DB: db}

	out, err := srv.memoryDiff("", 7, "proj2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(kestrel)") {
		t.Errorf("another project's change is listed with nothing saying whose it is:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "staging port") && strings.Contains(line, "(") {
			t.Errorf("this project's own change is labelled as foreign:\n%s", line)
		}
	}
}
