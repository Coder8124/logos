package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHooks(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hooks.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDoctorNamesACodexHooksFileCodexRefuses(t *testing.T) {
	path := writeHooks(t, `{"version":1,"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/bin/sh hook codex session-start"}]}]}}`)
	if got := hookFileProblem(path, "codex"); !strings.Contains(got, "version") {
		t.Fatalf("a versioned Codex file was not named: %q", got)
	}
}

func TestDoctorNamesAHookThatStartsTheServerInstead(t *testing.T) {
	path := writeHooks(t, `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/bin/sh mcp serve hook codex session-start"}]}]}}`)
	if got := hookFileProblem(path, "codex"); !strings.Contains(got, "MCP server") {
		t.Fatalf("an mcp serve hook was not named: %q", got)
	}
}

func TestDoctorNamesACodexHookInCursorsShape(t *testing.T) {
	path := writeHooks(t, `{"hooks":{"sessionStart":[{"command":"/bin/sh hook codex session-start"}]}}`)
	if got := hookFileProblem(path, "codex"); !strings.Contains(got, "never runs") {
		t.Fatalf("a flat Codex hook was not named: %q", got)
	}
}

func TestDoctorNamesAHookWhoseBinaryIsGone(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "logos")
	path := writeHooks(t, `{"version":1,"hooks":{"sessionStart":[{"command":"'`+gone+`' hook cursor session-start"}]}}`)
	if got := hookFileProblem(path, "cursor"); !strings.Contains(got, gone) {
		t.Fatalf("a hook on a deleted binary was not named: %q", got)
	}
}

func TestDoctorIsQuietAboutHooksThatWillRun(t *testing.T) {
	cases := map[string]string{
		"codex":  `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/bin/sh hook codex session-start"}]}]}}`,
		"cursor": `{"version":1,"hooks":{"sessionStart":[{"command":"npx -y logos hook cursor session-start"}]}}`,
	}
	for host, body := range cases {
		if got := hookFileProblem(writeHooks(t, body), host); got != "" {
			t.Errorf("%s: a working hook was reported: %q", host, got)
		}
	}
	if got := hookFileProblem(filepath.Join(t.TempDir(), "absent.json"), "codex"); got != "" {
		t.Errorf("no hooks file was reported: %q", got)
	}
}

func TestSetupLeavesNothingForTheHookCheckToReport(t *testing.T) {
	path := writeHooks(t, `{"version":1,"hooks":{"sessionStart":[{"command":"/bin/sh mcp serve hook codex session-start"}]}}`)
	if _, err := installHook(path, "codex", []string{"/bin/sh"}); err != nil {
		t.Fatal(err)
	}
	if got := hookFileProblem(path, "codex"); got != "" {
		t.Fatalf("setup left a problem behind: %q\n%s", got, mustRead(t, path))
	}
}
