package transcript

import "testing"

// A Turn's Input is the command for a shell tool and the path for everything
// else — that is the contract the harvest reads to list "files touched". A
// search query is neither, and letting it through meant a codebase_search for
// "activity.Dir" was harvested as a file the session edited: looksLikePath
// accepts any space-free token containing a dot. The checkpoint's Files list is
// the field the next agent reads as what changed, so a wrong entry there is
// worse than a missing one.
func TestACursorSearchQueryIsNotReportedAsAFileTheSessionTouched(t *testing.T) {
	tool := &cursorTool{
		Name:    "codebase_search",
		RawArgs: `{"query":"activity.Dir"}`,
	}

	got := tool.turn().Input

	if got != "" {
		t.Errorf("a search query was reported as a path: %q", got)
	}
}

// Dropping the query from Input must not drop the turn: a tool call that
// happened is part of the record whether or not we can say what it was given.
func TestACursorSearchStillAppearsAsATurn(t *testing.T) {
	tool := &cursorTool{
		Name:    "codebase_search",
		RawArgs: `{"query":"where is the retry budget"}`,
	}

	turn := tool.turn()

	if turn.Tool != "codebase_search" || turn.Role != "tool" {
		t.Errorf("the search turn was lost: %+v", turn)
	}
}

// The paths and commands the contract is actually about must still arrive.
func TestACursorEditStillReportsTheFileItTouched(t *testing.T) {
	for _, c := range []struct{ name, args, want string }{
		{"edit", `{"target_file":"internal/activity/activity.go"}`, "internal/activity/activity.go"},
		{"shell", `{"command":"go test ./internal/activity"}`, "go test ./internal/activity"},
	} {
		tool := &cursorTool{Name: c.name, RawArgs: c.args}
		if got := tool.turn().Input; got != c.want {
			t.Errorf("%s: input %q, want %q", c.name, got, c.want)
		}
	}
}
