package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/session"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// editRepo is a repository holding internal/parse/reader.go, and a vault with
// an index, for the edit hook to be run between.
func editRepo(t *testing.T) (repo, vault string, db *sql.DB) {
	t.Helper()
	repo = t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	commitTo(t, repo, "internal/parse/reader.go", "load all\n")
	vault = t.TempDir()
	ix, err := index.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	return repo, vault, ix.DB
}

func commitTo(t *testing.T, repo, name, body string) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "--", name},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "change " + name},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %s: %v %s", args[0], err, out)
		}
	}
}

func ruleOut(t *testing.T, db *sql.DB, vault, repo string, failed ...string) {
	t.Helper()
	_, head := gitstate.Head(repo)
	if err := session.Commit(db, vault, &session.Checkpoint{
		Project: projectFor(repo), Agent: "claude-code", Task: "work", Next: "carry on",
		Git: gitstate.State{Commit: head}, Failed: failed,
	}); err != nil {
		t.Fatal(err)
	}
}

// edit runs the hook as Claude Code would before an edit, and returns the
// context it injected, or "" when it printed nothing.
func edit(t *testing.T, vault, repo, sessionID, rel string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"session_id": sessionID, "cwd": repo, "hook_event_name": "PreToolUse", "tool_name": "Edit",
		"tool_input": map[string]string{"file_path": filepath.Join(repo, rel)},
	})
	var out bytes.Buffer
	editHookCmd(bytes.NewReader(payload), &out, vault)
	if out.Len() == 0 {
		return ""
	}
	var got struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("the hook printed something Claude Code cannot read: %v\n%s", err, out.String())
	}
	if got.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q, want PreToolUse — Claude Code drops context filed under another event", got.HookSpecificOutput.HookEventName)
	}
	return got.HookSpecificOutput.AdditionalContext
}

const readerRuling = "streaming the vendor export — internal/parse/reader.go loads the whole file into memory"

// The ruling reaches the agent at the one moment nobody thinks to ask for it,
// and only once: an agent editing reader.go twenty times in a session that saw
// the same line twenty times would learn to skip it.
func TestEditingAFileARulingNamesShowsTheRulingOnce(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, readerRuling)

	got := edit(t, vault, repo, "s1", "internal/parse/reader.go")
	if !strings.Contains(got, "streaming the vendor export") {
		t.Fatalf("editing the file the ruling names showed nothing about it:\n%q", got)
	}
	if !strings.Contains(got, "by claude-code") || !strings.Contains(got, "has not changed since") {
		t.Errorf("the line does not say who ruled it out and whether the file moved:\n%s", got)
	}
	if again := edit(t, vault, repo, "s1", "internal/parse/reader.go"); again != "" {
		t.Errorf("the same session was shown the ruling a second time:\n%s", again)
	}
	if other := edit(t, vault, repo, "s2", "internal/parse/reader.go"); other == "" {
		t.Error("a new session was never shown the ruling — once per session, not once ever")
	}

	events, _, err := usagepkg.Read(vault)
	if err != nil {
		t.Fatal(err)
	}
	if n := usagepkg.Sum(events, "").AtEdit; n != 2 {
		t.Errorf("the usage ledger counts %d rulings shown at edit, want 2 — a showing nobody can count reads as one that never happened", n)
	}
}

func TestEditingAFileNoRulingNamesSaysNothing(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, readerRuling)

	if got := edit(t, vault, repo, "s1", "internal/parse/writer.go"); got != "" {
		t.Errorf("editing a file no ruling names injected context:\n%s", got)
	}
}

// A ruling that spelled out a directory names that file, not every file that
// shares its name: cmd/reader.go is not internal/parse/reader.go.
func TestARulingAboutTheSameNameInAnotherDirectoryIsNotShown(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, "chunked reads — cmd/reader.go buffers the whole stream")

	if got := edit(t, vault, repo, "s1", "internal/parse/reader.go"); got != "" {
		t.Errorf("a ruling about cmd/reader.go was shown for internal/parse/reader.go:\n%s", got)
	}
}

// The hook keeps a cache so it does not read every checkpoint on every edit. A
// cache built before a ruling was recorded must not hide that ruling — the
// newest one is the one most likely to matter.
func TestARulingRecordedAfterTheCacheWasBuiltIsStillShown(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, "unrelated — go.sum churn")
	if got := edit(t, vault, repo, "s1", "internal/parse/reader.go"); got != "" {
		t.Fatalf("shown before any ruling named the file:\n%s", got)
	}

	ruleOut(t, db, vault, repo, readerRuling)
	if got := edit(t, vault, repo, "s1", "internal/parse/reader.go"); !strings.Contains(got, "streaming the vendor export") {
		t.Errorf("a ruling recorded after the cache was built never reached the edit:\n%q", got)
	}
}

// No index means a vault nobody has indexed, or one being rebuilt. The hook
// runs before every edit; failing, stalling, or quietly creating .logos/ there
// are all worse than saying nothing.
func TestTheEditHookWithNoIndexNeitherBlocksNorFails(t *testing.T) {
	repo := t.TempDir()
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, session.CheckpointDir, projectFor(repo)), 0o700); err != nil {
		t.Fatal(err)
	}

	if got := edit(t, vault, repo, "s1", "internal/parse/reader.go"); got != "" {
		t.Errorf("the hook injected context with no index:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(vault, ".logos")); !os.IsNotExist(err) {
		t.Errorf("the hook created .logos/ in a vault nobody indexed (stat: %v)", err)
	}

	// And a payload it cannot read is the same: nothing, not an error.
	var out bytes.Buffer
	editHookCmd(strings.NewReader("not json"), &out, vault)
	if out.Len() != 0 {
		t.Errorf("a malformed payload produced output: %s", out.String())
	}
}

// The ruling was true at the commit it was written against. Two commits to
// the file later it may be the one thing standing between the agent and the
// fix that now works, and the line has to say so.
func TestARulingWhoseFileChangedIsShownAsMayNoLongerHold(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, readerRuling)
	commitTo(t, repo, "internal/parse/reader.go", "read in chunks\n")
	commitTo(t, repo, "internal/parse/reader.go", "read in larger chunks\n")

	got := edit(t, vault, repo, "s1", "internal/parse/reader.go")
	if !strings.Contains(got, "changed in 2 commits since") || !strings.Contains(got, "may no longer hold") {
		t.Errorf("a ruling about a file rewritten since was shown as current:\n%s", got)
	}
}

// The rule every pre-tool hook is held to (CONTRIBUTING.md): one indexed
// lookup, no reading the vault's checkpoints one by one. Measured on a vault
// of 2,001 checkpoints the warm hook ran in 0.08s end to end where `logos
// tried` took 0.3–0.5s; the cold one, which builds the cache, in 0.62s.
//
// Timing alone is a flaky witness, so the property is checked directly too:
// once the cache is warm, every checkpoint file is made unreadable and the
// hook must still answer — it can only have read the index.
func TestTheEditHookStaysUnderItsBudgetOnALargeVault(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a vault of 2,000 checkpoints")
	}
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, readerRuling)

	dir := filepath.Join(vault, session.CheckpointDir, projectFor(repo))
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want the one seeded checkpoint, got %d (%v)", len(entries), err)
	}
	seed, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	filler := strings.Replace(string(seed), readerRuling, "an unrelated approach — go.sum churn", 1)
	for i := range 2000 {
		name := filepath.Join(dir, fmt.Sprintf("20200101-%06d-filler.md", i))
		if err := os.WriteFile(name, []byte(filler), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	run := func(sessionID string) (string, time.Duration) {
		start := time.Now()
		got := edit(t, vault, repo, sessionID, "internal/parse/reader.go")
		return got, time.Since(start)
	}
	if got, took := run("cold"); got == "" || took > editHookBudget {
		t.Fatalf("cold hook took %s (budget %s) and showed %q — past the budget it says nothing at all", took, editHookBudget, got)
	}

	files, _ := os.ReadDir(dir)
	for _, f := range files {
		os.Chmod(filepath.Join(dir, f.Name()), 0)
	}
	t.Cleanup(func() {
		for _, f := range files {
			os.Chmod(filepath.Join(dir, f.Name()), 0o600)
		}
	})
	got, took := run("warm")
	if !strings.Contains(got, "streaming the vendor export") {
		t.Fatalf("with the checkpoints unreadable the warm hook found nothing, so it was reading them:\n%q", got)
	}
	if took > 500*time.Millisecond {
		t.Errorf("warm hook took %s on 2,001 checkpoints; it was 0.08s end to end when measured", took)
	}
}
