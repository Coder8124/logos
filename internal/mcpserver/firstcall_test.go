package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
)

// firstCallServer is a server whose session is in project "kestrel", which has
// a checkpoint, connected as host — the hookless host by default.
func firstCallServer(t *testing.T, host string) *testClient {
	t.Helper()
	t.Setenv("LOGOS_PROJECT", "kestrel")
	c, db, vault := startServer(t)
	c.req("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": host, "version": "1"},
	})
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	cp := session.Checkpoint{Project: "kestrel", Agent: "cursor", Next: "quote the extruded option", TS: time.Now().Unix()}
	if err := session.Commit(db, vault, &cp); err != nil {
		t.Fatal(err)
	}
	return c
}

// A host with no session-start hook opened every session blind unless the
// model chose to call resume, which is the step it most often skips. The first
// thing it calls Logos for, whatever that is, now carries the handoff.
func TestTheFirstToolCallInAHostWithNoHookCarriesTheHandoff(t *testing.T) {
	c := firstCallServer(t, "claude-ai")

	out, isErr := c.callText(t, "list_projects", map[string]any{})
	if isErr {
		t.Fatalf("list_projects errored: %s", out)
	}
	if !strings.Contains(out, "quote the extruded option") {
		t.Errorf("the first call in a hookless host did not carry where work left off:\n%s", out)
	}
	if !strings.Contains(out, "no session-start hook") {
		t.Errorf("the handoff arrived without saying why it is there:\n%s", out)
	}
}

// Once, not on every call: the handoff is a few thousand tokens, and repeated
// it would bury every answer after it.
func TestTheHandoffIsCarriedOnlyOnce(t *testing.T) {
	c := firstCallServer(t, "claude-ai")

	c.callText(t, "list_projects", map[string]any{})
	out, _ := c.callText(t, "list_projects", map[string]any{})
	if strings.Contains(out, "quote the extruded option") {
		t.Errorf("the second call carried the handoff again:\n%s", out)
	}
}

// A model that resumed on its own already has the handoff; adding it to the
// next call would hand it over twice.
func TestAModelThatCalledResumeFirstIsNotHandedItAgain(t *testing.T) {
	c := firstCallServer(t, "claude-ai")

	if out, isErr := c.callText(t, "resume", map[string]any{"project": "kestrel"}); isErr || strings.Count(out, "quote the extruded option") != 1 {
		t.Fatalf("resume as the first call: isErr=%v, want the handoff once:\n%s", isErr, out)
	}
	if out, _ := c.callText(t, "list_projects", map[string]any{}); strings.Contains(out, "quote the extruded option") {
		t.Errorf("the call after resume carried the handoff again:\n%s", out)
	}
}

// Claude Code's plugin hook already opened the session with the handoff.
func TestAHostWhoseHookHandedOverIsNotHandedItAgain(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	installed := `{"plugins":{"logos@noeton":[{"scope":"user","version":"0.5.8"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "plugins", "installed_plugins.json"), []byte(installed), 0o644); err != nil {
		t.Fatal(err)
	}
	c := firstCallServer(t, "claude-code")

	if out, _ := c.callText(t, "list_projects", map[string]any{}); strings.Contains(out, "quote the extruded option") {
		t.Errorf("a hooked host was handed the handoff a second time:\n%s", out)
	}
}

// Cursor only has a hook if setup wrote one. Cursor wired by hand opens blind
// like any hookless host, and is treated as one.
func TestCursorWithoutTheHookInstalledIsHandedOver(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := firstCallServer(t, "cursor-vscode")

	if out, _ := c.callText(t, "list_projects", map[string]any{}); !strings.Contains(out, "quote the extruded option") {
		t.Errorf("Cursor with no hooks file was not handed over:\n%s", out)
	}
}

// A cancelled call's reply is dropped unread; the handoff on it is still owed.
func TestAHandoffOnAResultTheHostNeverReceivedIsCarriedAgain(t *testing.T) {
	s := &Session{}
	s.startOffered = true
	s.startSent(false)
	if s.started {
		t.Error("a handoff dropped with a cancelled reply was counted as delivered")
	}
	s.startOffered = true
	s.startSent(true)
	if !s.started {
		t.Error("a delivered handoff was not counted")
	}
}

// The other half: Cursor whose hooks file holds logos's session-start hook
// already opened the session with the handoff.
func TestCursorWithTheHookInstalledIsNotHandedItAgain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := `{"version":1,"hooks":{"sessionStart":[{"command":"/usr/local/bin/logos hook cursor session-start"}]}}`
	if err := os.WriteFile(filepath.Join(home, ".cursor", "hooks.json"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	c := firstCallServer(t, "cursor-vscode")

	if out, _ := c.callText(t, "list_projects", map[string]any{}); strings.Contains(out, "quote the extruded option") {
		t.Errorf("Cursor with the hook installed was handed over twice:\n%s", out)
	}
}

// Claude Code registered with `claude mcp add`, without the plugin, has no
// session-start hook: being Claude Code is not the same as being hooked.
func TestClaudeCodeWithoutThePluginIsHandedOver(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	c := firstCallServer(t, "claude-code")

	if out, _ := c.callText(t, "list_projects", map[string]any{}); !strings.Contains(out, "quote the extruded option") {
		t.Errorf("Claude Code with no plugin was not handed over:\n%s", out)
	}
}

// A session in a project the vault does not hold yet has no earlier work. Its
// own first write creates the project, and the call after it must not hand the
// model what it just wrote as where a previous agent left off.
func TestASessionThatCreatesItsProjectIsNotHandedItsOwnWork(t *testing.T) {
	c := firstCallServer(t, "claude-ai")
	t.Setenv("LOGOS_PROJECT", "snowflake")

	out, isErr := c.callText(t, "remember", map[string]any{"project": "snowflake", "text": "the drift flag is per tenant"})
	if isErr {
		t.Fatalf("remember errored: %s", out)
	}
	if strings.Contains(out, "no session-start hook") {
		t.Errorf("the write that created the project carried a handoff of itself:\n%s", out)
	}
	if out, _ := c.callText(t, "list_projects", map[string]any{}); strings.Contains(out, "no session-start hook") {
		t.Errorf("the call after it handed the session its own new work:\n%s", out)
	}
}

// A model whose first call is a checkpoint has finished the work the handoff
// was for; the checkpoint it gets back on the next call would be its own.
func TestAFirstCallThatCheckpointsIsNotHandedItsOwnCheckpoint(t *testing.T) {
	c := firstCallServer(t, "claude-ai")

	for _, tool := range []string{"checkpoint", "list_projects"} {
		args := map[string]any{}
		if tool == "checkpoint" {
			args = map[string]any{"project": "kestrel", "agent": "desktop", "state": "shipped the quote", "next": "invoice it"}
		}
		if out, _ := c.callText(t, tool, args); strings.Contains(out, "no session-start hook") {
			t.Errorf("%s after a first checkpoint carried a handoff:\n%s", tool, out)
		}
	}
}
