package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no file at %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", path, err, raw)
	}
	return out
}

func hookCommands(t *testing.T, path string) []string {
	t.Helper()
	hooks, _ := readJSON(t, path)["hooks"].(map[string]any)
	events, _ := hooks["sessionStart"].([]any)
	var out []string
	for _, e := range events {
		m, _ := e.(map[string]any)
		s, _ := m["command"].(string)
		out = append(out, s)
	}
	return out
}

// Registering the MCP server gives a model tools it may or may not call. What
// makes continuity happen without the user asking for it is a session-start
// hook, and Cursor has one — so a Cursor user was getting the weaker half of
// Logos while the report said "connected" exactly as it did for Claude Code.
func TestCursorGetsASessionStartHookAndNotOnlyTheServer(t *testing.T) {
	home := fakeHome(t, ".cursor")

	r := Install(server(), []Host{hostNamed(t, "Cursor")})

	if r[0].Outcome == Failed {
		t.Fatalf("Cursor was not wired: %v", r[0].Err)
	}
	if r[0].HookErr != nil {
		t.Fatalf("hook: %v", r[0].HookErr)
	}
	cmds := hookCommands(t, filepath.Join(home, ".cursor", "hooks.json"))
	if len(cmds) != 1 || !strings.Contains(cmds[0], "hook cursor session-start") {
		t.Errorf("Cursor has no logos session-start hook: %q", cmds)
	}
	// Invariant 3: a feature that installed itself silently reads as one that
	// did not install at all.
	if r[0].Hooked != Registered {
		t.Errorf("the hook was installed but not reported: %q", r[0].Hooked)
	}
	if r[0].Tier != TierHooks {
		t.Errorf("tier = %q, want %q", r[0].Tier, TierHooks)
	}
}

// A hooks file is the user's own. Overwriting it would take out whatever else
// they run at session start, and that is not a loss setup gets to cause.
func TestInstallingTheHookKeepsWhatTheUserAlreadyRunsAtSessionStart(t *testing.T) {
	home := fakeHome(t, ".codex")
	path := filepath.Join(home, ".codex", "hooks.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"hooks":{"sessionStart":[{"command":"theirs.sh"}],"stop":[{"command":"bye.sh"}]},"theirSetting":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installHook(path, "codex", "/usr/local/bin/logos"); err != nil {
		t.Fatal(err)
	}

	cmds := hookCommands(t, path)
	if len(cmds) != 2 || cmds[0] != "theirs.sh" {
		t.Errorf("the user's own session-start hook is gone: %q", cmds)
	}
	file := readJSON(t, path)
	if file["theirSetting"] != true {
		t.Errorf("the rest of the file was dropped: %v", file)
	}
	hooks, _ := file["hooks"].(map[string]any)
	if _, ok := hooks["stop"]; !ok {
		t.Errorf("their other events were dropped: %v", hooks)
	}
}

// Running setup again — after a `brew upgrade`, or with a new --vault — used to
// be the safe thing to do. An appended hook would leave two, the older one
// pointing at a binary that may no longer be there.
func TestRunningSetupTwiceLeavesOneHookPointingAtThisLogos(t *testing.T) {
	home := fakeHome(t, ".cursor")
	path := filepath.Join(home, ".cursor", "hooks.json")

	if _, err := installHook(path, "cursor", "/old/bin/logos"); err != nil {
		t.Fatal(err)
	}
	outcome, err := installHook(path, "cursor", "/new/bin/logos")
	if err != nil {
		t.Fatal(err)
	}

	cmds := hookCommands(t, path)
	if len(cmds) != 1 {
		t.Fatalf("setup added a second hook: %q", cmds)
	}
	if !strings.HasPrefix(cmds[0], "/new/bin/logos ") {
		t.Errorf("the hook still runs the old binary: %q", cmds[0])
	}
	if outcome != Updated {
		t.Errorf("outcome = %q, want %q", outcome, Updated)
	}
}

// A path with a space in it is a path the host's shell splits in two, and the
// hook then runs a command that does not exist — silently, at every session.
func TestAHookCommandQuotesABinaryPathWithASpaceInIt(t *testing.T) {
	home := fakeHome(t, ".cursor")
	path := filepath.Join(home, ".cursor", "hooks.json")

	if _, err := installHook(path, "cursor", "/Users/a b/bin/logos"); err != nil {
		t.Fatal(err)
	}

	if cmds := hookCommands(t, path); !strings.HasPrefix(cmds[0], `'/Users/a b/bin/logos' `) {
		t.Errorf("the path was left for the shell to split: %q", cmds[0])
	}
}

// A malformed hooks file is refused, not replaced: the same promise mergeJSON
// makes about an MCP config.
func TestAHooksFileThatIsNotJSONIsLeftAlone(t *testing.T) {
	home := fakeHome(t, ".cursor")
	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installHook(path, "cursor", "/usr/local/bin/logos"); err == nil {
		t.Fatal("a file logos cannot read was rewritten anyway")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "{not json" {
		t.Errorf("the file was changed: %q", raw)
	}
}

// A host with no session-start hook is not broken, it is thinner, and the
// report has to say which it is rather than calling both "connected".
func TestAHostWithNoHookIsReportedAsTheThinnerIntegration(t *testing.T) {
	if got := TierOf(hostNamed(t, "Devin")); got != TierMCP {
		t.Errorf("Devin tier = %q, want %q", got, TierMCP)
	}
	if got := TierOf(hostNamed(t, "Claude Code")); got != TierPlugin {
		t.Errorf("Claude Code tier = %q, want %q", got, TierPlugin)
	}
}

// A hook left behind after uninstall runs a logos that is no longer there, and
// the host reports that failure at the user on every single session.
func TestUninstallTakesTheSessionStartHookOutToo(t *testing.T) {
	home := fakeHome(t, ".cursor")
	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"hooks":{"sessionStart":[{"command":"theirs.sh"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installHook(path, "cursor", "/usr/local/bin/logos"); err != nil {
		t.Fatal(err)
	}

	r := Uninstall([]Host{hostNamed(t, "Cursor")})

	if len(r) != 1 || !r[0].Unhooked {
		t.Fatalf("the hook was left running a logos that is gone: %+v", r)
	}
	cmds := hookCommands(t, path)
	if len(cmds) != 1 || cmds[0] != "theirs.sh" {
		t.Errorf("uninstall did not leave the user's own hooks alone: %q", cmds)
	}
}
