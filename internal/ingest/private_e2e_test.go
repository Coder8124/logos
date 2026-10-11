package ingest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/activity"
	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

// A person who types <private>…</private> has said what Logos must never keep.
// That holds on every path text takes in — the hook's capture line, a
// transcript read for a harvest or an auto record, an import — and each one
// says it kept something out, or the person cannot tell the span was honoured.
func TestAPrivateSpanIsNeverCapturedHarvestedOrImported(t *testing.T) {
	const pin, pw = "bank pin is 4417", "hunter2-staging"

	e, err := activity.FromHook("UserPromptSubmit",
		[]byte(`{"session_id":"s1","cwd":"/work/shop","prompt":"<private>my `+pin+`</private> fix the checkout crash"}`), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.Summary, "4417") {
		t.Errorf("the capture line kept the private span: %q", e.Summary)
	}
	if !strings.Contains(e.Summary, "fix the checkout crash") || !strings.Contains(e.Summary, "Kept out 1 <private> span") {
		t.Errorf("the capture line should keep the request and say a span was kept out: %q", e.Summary)
	}

	dir := filepath.Join(t.TempDir(), "-work-shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "priv-1.jsonl")
	lines := []string{
		`{"type":"user","sessionId":"priv-1","cwd":"/work/shop","timestamp":"2026-10-10T10:00:00Z","message":{"role":"user","content":"<private>my ` + pin + `</private> fix the checkout crash"}}`,
		`{"type":"assistant","sessionId":"priv-1","cwd":"/work/shop","timestamp":"2026-10-10T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"login --password <private>` + pw + `</private> && go test ./cart"}}]}}`,
		`{"type":"user","sessionId":"priv-1","cwd":"/work/shop","timestamp":"2026-10-10T10:02:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := transcript.ReadFile("claude-code", path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Private != 2 {
		t.Errorf("read %d private spans, want 2", s.Private)
	}

	h := ingest.Harvest(s)
	if md := h.Markdown(); strings.Contains(md, "4417") || strings.Contains(md, pw) {
		t.Errorf("the harvest kept a private span:\n%s", md)
	} else if !strings.Contains(md, "2 <private> span(s) kept out") {
		t.Errorf("the harvest does not say what it kept out:\n%s", md)
	}

	vault := t.TempDir()
	c, _, err := ingest.AutoCheckpoint(vault, s, "shop")
	if err != nil || c == nil {
		t.Fatalf("recorded %v, %v", c, err)
	}
	filepath.Walk(vault, func(p string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "4417") || strings.Contains(string(b), pw) {
				t.Errorf("a private span reached the vault in %s", p)
			}
		}
		return nil
	})
	if notice := ingest.SweepNotice([]session.Checkpoint{*c}, nil, nil); !strings.Contains(notice, "Kept out 2 <private> span(s)") {
		t.Errorf("the sweep's notice does not say what it kept out: %q", notice)
	}

	imported, err := transcript.ReadInterchange(strings.NewReader(
		`{"harness":"other","id":"imp-1","messages":[{"role":"user","text":"<private>my ` + pin + `</private> ship it"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(imported.Turns[0].Text, "4417") || imported.Private != 1 {
		t.Errorf("the import kept the private span: %q (%d counted)", imported.Turns[0].Text, imported.Private)
	}
}
