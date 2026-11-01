package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/session"
)

// commitConversation writes a checkpoint with a host session the way a hooked
// host does, and closes the index so the test can delete it underneath.
func commitConversation(t *testing.T, vault string, c *session.Checkpoint) {
	t.Helper()
	ix, err := index.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, vault, c); err != nil {
		t.Fatal(err)
	}
}

// The folder layout is only safe if it is pure vault: a conversation found by
// its id or its name after the cache is gone proves nothing about it lived in
// SQLite alone (CLAUDE.md invariant 1).
func TestASessionFolderSurvivesDeletingTheIndex(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	t.Setenv("LOGOS_EMBED", "off")
	t.Setenv("LOGOS_PROJECT", "kestrel")
	commitConversation(t, vault, &session.Checkpoint{Project: "kestrel", Agent: "a", Task: "fix checkout", Intent: "Checkout crashes lose orders", Next: "replay the failed order queue", HostSession: "sess-checkout"})
	commitConversation(t, vault, &session.Checkpoint{Project: "kestrel", Agent: "a", Task: "speed up search", Intent: "Search takes four seconds", Next: "profile the tokenizer", HostSession: "sess-search"})

	if err := os.RemoveAll(filepath.Join(vault, ".logos")); err != nil {
		t.Fatal(err)
	}
	if err := runIndex(false); err != nil {
		t.Fatalf("reindex after deleting the cache: %v", err)
	}

	for _, q := range []string{"sess-checkout", "lose orders"} {
		out := captureStdout(t, func() {
			if err := runResume([]string{"kestrel", q}); err != nil {
				t.Fatalf("resume %q: %v", q, err)
			}
		})
		if !strings.Contains(out, "replay the failed order queue") || strings.Contains(out, "profile the tokenizer") {
			t.Errorf("resume %q after deleting the index is not that one conversation:\n%s", q, out)
		}
	}
	if _, err := os.Stat(filepath.Join(vault, session.CheckpointDir, "kestrel", session.ConvosDir, "sess-checkout")); err != nil {
		t.Errorf("the conversation folder is gone after reindexing: %v", err)
	}
}

// The SessionStart hook runs `logos resume <project>` and injects its stdout.
// A picker there would wait for a keypress nobody can send until the host
// kills the hook, so the hook must lose nothing it delivers today — even on a
// host that one day spawns hooks on a terminal.
func TestThePickerNeverRunsWhenInvokedFromTheSessionStartHook(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	t.Setenv("LOGOS_EMBED", "off")
	t.Setenv("LOGOS_PROJECT", "kestrel")
	commitConversation(t, vault, &session.Checkpoint{Project: "kestrel", Agent: "a", Task: "fix checkout", Next: "replay the failed order queue", HostSession: "sess-checkout"})

	asked := 0
	was := interactive
	interactive = func() bool { asked++; return true }
	defer func() { interactive = was }()

	// Sanity: without the hook signal this terminal would be offered the picker.
	if !pickerAllowed(false) {
		t.Fatal("a claimed terminal outside the hook was refused the picker")
	}

	t.Setenv("LOGOS_HOOK", "1")
	asked = 0
	out := captureStdout(t, func() {
		if err := runResume([]string{"kestrel"}); err != nil {
			t.Fatal(err)
		}
	})
	if asked != 0 {
		t.Errorf("resume consulted the terminal %d time(s) from the hook", asked)
	}
	if !strings.Contains(out, "## Where we left off") || !strings.Contains(out, "replay the failed order queue") {
		t.Errorf("the hook's resume is not the ordinary whole-project pack:\n%s", out)
	}

	// The Go hook spawns resume as a child process; the child only knows it is
	// in a hook if the parent says so.
	os.Unsetenv("LOGOS_HOOK")
	echo := fakeLogos(t, `echo "## Where we left off"; echo "hook=$LOGOS_HOOK"`)
	if got := handoffFrom(echo, t.TempDir()); !strings.Contains(got, "hook=1") {
		t.Errorf("the hook's resume child was not told it runs in a hook:\n%s", got)
	}
}

// The picker's keys, driven without a terminal: Enter alone is the old
// behaviour (the whole project), arrows move, q is a cancel, never a pick,
// and a bare Esc goes back rather than cancelling.
func TestThePickerMovesWithArrowsCancelsOnQAndGoesBackOnEsc(t *testing.T) {
	rows := []string{"whole", "one", "two"}
	var sink strings.Builder
	if i, res := runPicker(strings.NewReader("\r"), &sink, "t", "h", rows, 80); res != picked || i != 0 {
		t.Errorf("Enter alone picked %d res=%v, want the whole project", i, res)
	}
	if i, res := runPicker(strings.NewReader("\x1b[B\x1b[B\x1b[A\r"), &sink, "t", "h", rows, 80); res != picked || i != 1 {
		t.Errorf("down, down, up, Enter picked %d res=%v, want 1", i, res)
	}
	if i, res := runPicker(strings.NewReader("jjjjj\r"), &sink, "t", "h", rows, 80); res != picked || i != 2 {
		t.Errorf("moving past the end picked %d res=%v, want the last row", i, res)
	}
	if _, res := runPicker(strings.NewReader("jq"), &sink, "t", "h", rows, 80); res != cancelled {
		t.Errorf("q left the menu with %v, want a cancel", res)
	}
	if _, res := runPicker(strings.NewReader(""), &sink, "t", "h", rows, 80); res != cancelled {
		t.Errorf("closed input left the menu with %v, want a cancel", res)
	}
	if _, res := runPicker(strings.NewReader("j\x1b"), &sink, "t", "h", rows, 80); res != wentBack {
		t.Errorf("a bare Esc left the menu with %v, want back", res)
	}
}

// An ambiguous name at the command line is a non-zero exit with the
// candidates on stdout — a script that guessed would resume the wrong work.
func TestAnAmbiguousNameAtTheCommandLineExitsNonZero(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	t.Setenv("LOGOS_EMBED", "off")
	t.Setenv("LOGOS_PROJECT", "kestrel")
	commitConversation(t, vault, &session.Checkpoint{Project: "kestrel", Agent: "a", Task: "t1", Intent: "Fix the auth redirect", HostSession: "one"})
	commitConversation(t, vault, &session.Checkpoint{Project: "kestrel", Agent: "a", Task: "t2", Intent: "Fix the auth token expiry", HostSession: "two"})

	var err error
	out := captureStdout(t, func() { err = runResume([]string{"kestrel", "auth"}) })
	if err == nil {
		t.Fatal("an ambiguous name exited zero")
	}
	if !strings.Contains(out, "auth redirect") || !strings.Contains(out, "auth token expiry") || strings.Contains(out, "## Where we left off") {
		t.Errorf("want both candidates listed and nothing resumed:\n%s", out)
	}
}

// A script told "name one by id" has to be handed the ids, in one parseable
// document — not a bare error block after the candidates were thrown away.
func TestAnAmbiguousNameInJSONCarriesItsCandidates(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	t.Setenv("LOGOS_EMBED", "off")
	t.Setenv("LOGOS_PROJECT", "kestrel")
	commitConversation(t, vault, &session.Checkpoint{Project: "kestrel", Agent: "a", Task: "t1", Intent: "Fix the auth redirect", HostSession: "one"})
	commitConversation(t, vault, &session.Checkpoint{Project: "kestrel", Agent: "a", Task: "t2", Intent: "Fix the auth token expiry", HostSession: "two"})

	var err error
	out := captureStdout(t, func() { err = runResume([]string{"kestrel", "auth", "--json"}) })
	var printed jsonPrinted
	if !errors.As(err, &printed) {
		t.Fatalf("want a failure whose JSON is already printed, got %v", err)
	}
	var doc struct {
		Candidates []struct{ ID string } `json:"candidates"`
	}
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("not one JSON document: %v\n%s", jerr, out)
	}
	if len(doc.Candidates) != 2 {
		t.Fatalf("want both candidate ids, got %+v", doc.Candidates)
	}
}
