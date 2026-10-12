package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/activity"
	"github.com/Coder8124/logos/internal/scope"
	"github.com/Coder8124/logos/internal/session"
)

// notice runs what the session-start hook runs on first contact.
func notice(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	return captureStdout(t, func() {
		if err := runActivity([]string{"notice", dir}); err != nil {
			t.Fatalf("notice: %v", err)
		}
	})
}

// recordUnasked records a hook event without first switching recording on, the
// way the hook does on a vault nobody has asked about.
func recordUnasked(t *testing.T, event, payload string) {
	t.Helper()
	restore := setStdin(t, []byte(payload))
	defer restore()
	captureStdout(t, func() {
		if err := recordActivity([]string{"--event", event, "--project", "shop"}); err != nil {
			t.Fatalf("record %s: %v", event, err)
		}
	})
}

// The failure 1g is for: a repo worked in with the plugin installed and no
// setup run, and nothing recorded, silently. No vault, no deny marker, and the
// session's work still has to come back as a checkpoint.
func TestARepoWithNoVaultAndNoDenyMarkerIsCheckpointedOnFirstContact(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("LOGOS_VAULT", "")
	t.Setenv("LOGOS_PROJECT", "")
	repo := filepath.Join(t.TempDir(), "shop")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(prev) })

	if said := notice(t); !strings.Contains(said, "Logos started tracking this repo") {
		t.Errorf("first contact said nothing: %q", said)
	}
	if _, err := os.Stat(filepath.Join(home, "logos")); err != nil {
		t.Fatalf("first contact made no vault: %v", err)
	}
	recordUnasked(t, "UserPromptSubmit", `{"session_id":"s-first","prompt":"fix the checkout crash"}`)
	recordUnasked(t, "PostToolUse", `{"session_id":"s-first","tool_name":"Edit","tool_input":{"file_path":"cart.go"}}`)

	if out := endSession(t, "s-first"); !strings.HasPrefix(out, "auto-checkpointed") {
		t.Errorf("the first session in an untouched repo was not checkpointed: %q", out)
	}
	if c, _ := session.Latest(vaultPath(), "shop"); c == nil {
		t.Error("no checkpoint in the vault")
	}
}

// Deny is checked before anything is written, not filtered afterwards: no
// activity line, no checkpoint, no note.
func TestLogosDenyStopsAllFutureWritesToThatRepo(t *testing.T) {
	work := standIn(t, "shop")
	if err := runDeny(nil); err != nil {
		t.Fatal(err)
	}
	if !scope.Denied(mustGetwd(t)) {
		t.Fatal("deny wrote no marker this repo reads")
	}

	hookEvent(t, "PostToolUse", `{"session_id":"s-deny","tool_name":"Edit","tool_input":{"file_path":"cart.go"}}`)
	endSession(t, "s-deny")

	if got, _ := activity.Read(work, activity.Query{Limit: 10}); len(got) != 0 {
		t.Errorf("a denied repo's tool call reached the activity log: %+v", got)
	}
	if c, _ := session.Latest(work, "shop"); c != nil {
		t.Errorf("a denied repo was checkpointed: %+v", c)
	}
	if _, err := os.Stat(filepath.Join(work, "sessions", "shop")); err == nil {
		t.Error("a denied repo left a sessions directory in the vault")
	}
}

func TestLogosDenyUndoResumesTrackingNextContact(t *testing.T) {
	standIn(t, "shop")
	if err := runDeny(nil); err != nil {
		t.Fatal(err)
	}
	if err := runDeny([]string{"--undo"}); err != nil {
		t.Fatal(err)
	}
	if scope.Denied(mustGetwd(t)) {
		t.Fatal("--undo left the repo denied")
	}

	hookEvent(t, "PostToolUse", `{"session_id":"s-undo","tool_name":"Edit","tool_input":{"file_path":"cart.go"}}`)
	if out := endSession(t, "s-undo"); !strings.HasPrefix(out, "auto-checkpointed") {
		t.Errorf("tracking did not resume after --undo: %q", out)
	}
}

// Said once, on the first contact. A banner on every session is noise the user
// learns to skip, and then skips the lines that matter.
func TestFirstContactAnnouncesItselfOnceNotOnEverySession(t *testing.T) {
	standIn(t, "shop")
	first := notice(t)
	if !strings.Contains(first, "Logos started tracking this repo") || !strings.Contains(first, "logos deny") {
		t.Fatalf("first contact did not announce itself with the way out: %q", first)
	}
	if again := notice(t); strings.Contains(again, "started tracking") {
		t.Errorf("the second session announced it again: %q", again)
	}

	// A repo Logos already holds checkpoints for is not a first contact, even
	// on a vault that predates the announcement.
	standIn(t, "widgets")
	if err := runCheckpoint([]string{"--task", "cache", "--next", "ship"}); err != nil {
		t.Fatal(err)
	}
	if said := notice(t); strings.Contains(said, "started tracking") {
		t.Errorf("a repo with history was announced as new: %q", said)
	}
}

// The user asked for exactly this, so a denied repo is not told about it on
// every hook: every entry point returns cleanly, prints nothing and writes
// nothing.
func TestADeniedRepoReturnsCleanlyWithNoWriteAndNoWarningSpam(t *testing.T) {
	work := standIn(t, "shop")
	if err := runDeny(nil); err != nil {
		t.Fatal(err)
	}
	if err := activity.SetRecording(work, true); err != nil {
		t.Fatal(err)
	}

	var said string
	said += captureStderr(t, func() {
		said += notice(t)
		said += captureStdout(t, func() {
			if err := runProjectName(nil); err != nil {
				t.Errorf("project-name: %v", err)
			}
		})
		recordUnasked(t, "Stop", `{"session_id":"s-quiet"}`)
		said += endSession(t, "s-quiet")
	})
	if strings.TrimSpace(said) != "" {
		t.Errorf("a denied repo printed: %q", said)
	}
	if entries, _ := os.ReadDir(filepath.Join(work, "sessions")); len(entries) != 0 {
		t.Errorf("a denied repo wrote to the vault: %v", entries)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	d, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// The MCP half of the promise: an agent in a denied repo is offered no memory
// tools, and a call a host makes anyway neither writes nor comes back as an
// error the model reports on every turn.
func TestADeniedRepoGetsAnMCPServerWithNoToolsThatWritesNothing(t *testing.T) {
	work := standIn(t, "shop")
	if err := runDeny(nil); err != nil {
		t.Fatal(err)
	}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"note_progress","arguments":{"text":"secret work"}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	replies := map[float64]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var r map[string]any
		if json.Unmarshal([]byte(line), &r) == nil {
			if id, ok := r["id"].(float64); ok {
				replies[id] = r
			}
		}
	}
	if tools, _ := replies[2]["result"].(map[string]any)["tools"].([]any); len(tools) != 0 {
		t.Errorf("a denied repo was offered %d tools", len(tools))
	}
	call, _ := replies[3]["result"].(map[string]any)
	if call == nil || call["isError"] == true || !strings.Contains(out.String(), "logos deny") {
		t.Errorf("a call in a denied repo did not answer plainly and say why:\n%s", out.String())
	}
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Errorf("the MCP server touched the vault of a denied repo: %v", entries)
	}
}

// Flag order is not something a user remembers. `logos deny --undo ~/work/api`
// run from home used to undo home, say it was never denied, and leave the repo
// off while the user believed it was back on.
func TestDenyUndoWithTheDirectoryAfterTheFlagUndoesThatDirectory(t *testing.T) {
	standIn(t, "shop")
	repo := mustGetwd(t)
	if err := runDeny(nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := runDeny([]string{"--undo", repo}); err != nil {
		t.Fatal(err)
	}
	if scope.Denied(repo) {
		t.Error("`logos deny --undo <dir>` left <dir> denied")
	}
}
