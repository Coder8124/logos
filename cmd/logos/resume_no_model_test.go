package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/session"
)

// The SessionStart hook runs `logos resume` under Claude Code's 10 s hook
// timeout, and resume embedded its query three times on the way to a handoff
// that needs no vectors. A runtime answering each embed in 5 s took the hook to
// 15 s; Claude Code killed it and the session started with no restore and no
// reason. LOGOS_EMBED=off is how the hook asks for none, and resume ignored it.
func TestResumeWithEmbeddingsOffAsksTheRuntimeForNothing(t *testing.T) {
	runtime := newRecordingRuntime(t, "nomic-embed-text")
	old := provider.LocalEndpoints
	provider.LocalEndpoints = []provider.LocalEndpoint{{Name: "Fake", URL: runtime.URL}}
	t.Cleanup(func() { provider.LocalEndpoints = old })
	t.Setenv("LOGOS_RUNTIME", "")
	t.Setenv("LOGOS_EMBED", "off")

	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, vault, &session.Checkpoint{
		Project: "kestrel-one", Next: "refresh the tariff table",
	}); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	out := captureStdout(t, func() {
		if err := runResume([]string{"kestrel-one"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "refresh the tariff table") {
		t.Fatalf("resume lost the handoff:\n%s", out)
	}
	if got := runtime.embedded(); len(got) != 0 {
		t.Errorf("resume with LOGOS_EMBED=off sent %d embeddings requests", len(got))
	}
}

// The other half: the hook has to ask. Resume honouring LOGOS_EMBED=off does
// nothing for a session start that never sets it.
func TestTheSessionStartHookRunsResumeWithEmbeddingsOff(t *testing.T) {
	hook, err := filepath.Abs("../../plugin/hooks/session-start.sh")
	if err != nil {
		t.Fatal(err)
	}
	bin, seen := t.TempDir(), filepath.Join(t.TempDir(), "embed")
	fakeProgram(t, bin, "logos", `case "$1" in
--version) echo "logos 0.4.99" ;;
project-name) echo kestrel-one ;;
resume) printf '%s' "${LOGOS_EMBED-unset}" > '`+seen+`'; echo "## Where we left off" ;;
esac`)
	project := t.TempDir()

	cmd := exec.Command("/bin/bash", hook)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_PROJECT_DIR=" + project}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook: %v\n%s", err, out)
	}
	got, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the hook never ran resume: %v", err)
	}
	if string(got) != "off" {
		t.Errorf("the hook ran resume with LOGOS_EMBED=%s, want off", got)
	}
}
