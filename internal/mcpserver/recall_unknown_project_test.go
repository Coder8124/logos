package mcpserver

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

// A typo'd project name and a real project with nothing recorded on the
// subject returned the same sentence — "No relevant memories in X" — and the
// agent draws the same conclusion from either: this work has no recorded
// facts, carry on without them. Only one of those is true, and the wrong one
// is a failure shaped like a result.
func TestRecallSaysSoWhenTheProjectItWasGivenDoesNotExist(t *testing.T) {
	db := testDB(t)
	srv := &Server{DB: db, vault: t.TempDir()}
	sess := &Session{Server: srv}

	m := memory.Memory{Text: "billing retries twice", Kind: memory.Context, Project: "kestrel", Source: "test"}
	if _, err := memory.Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	out, err := sess.recall("billing", 5, "kestral", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No project named") {
		t.Errorf("a project that does not exist answered as if it existed and knew nothing:\n%s", out)
	}
	if !strings.Contains(out, "kestral") {
		t.Errorf("the answer does not name the project that was not found:\n%s", out)
	}
}

// The real case must keep its own answer: a project that exists and has
// nothing on the subject is not an error, and telling the agent the project is
// unknown would be the same lie in the other direction.
func TestRecallStillReportsAnEmptyResultForAProjectThatExists(t *testing.T) {
	db := testDB(t)
	srv := &Server{DB: db, vault: t.TempDir()}
	sess := &Session{Server: srv}

	m := memory.Memory{Text: "the regulator browns out at 3.1V", Kind: memory.Context, Project: "kestrel", Source: "test"}
	if _, err := memory.Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	out, err := sess.recall("zzqx-nothing-matches-this", 5, "kestrel", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "No project named") {
		t.Errorf("a project that exists was reported as unknown:\n%s", out)
	}
}
