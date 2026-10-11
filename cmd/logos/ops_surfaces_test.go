package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/mcpserver"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
)

// noRuntime pins a test to the lexical path: no runtime to find, no embedding
// to wait on, so both surfaces see the same evidence.
func noRuntime(t *testing.T) {
	t.Helper()
	t.Setenv("LOGOS_RUNTIME", "")
	t.Setenv("LOGOS_WORKTREE", "")
	t.Setenv("LOGOS_PROJECT", "")
	old := provider.LocalEndpoints
	provider.LocalEndpoints = nil
	t.Cleanup(func() { provider.LocalEndpoints = old })
}

// recallFixture is a vault in the state a recall has the most to say about: a
// memory that answers, and a review queue the user has edited by hand — two
// proposals queued, one line deleted, which rejects it on the next read.
func recallFixture(t *testing.T) string {
	t.Helper()
	vault := t.TempDir()
	ix, err := index.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Store(ix.DB, nil, "", &memory.Memory{
		Text: "the kestrel staging port is 9090", Kind: memory.Fact, Source: "manual", Project: "kestrel",
	}); err != nil {
		t.Fatal(err)
	}
	for _, fact := range []string{"the CFO is Priya", "billing runs on the first"} {
		if _, err := memory.Store(ix.DB, nil, "", &memory.Memory{
			Text: fact, Kind: memory.Fact, Source: "mcp", Project: "kestrel", Quarantined: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(vault, memory.Dir, memory.PendingFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.SplitAfter(string(raw), "\n") {
		if !strings.Contains(line, "the CFO is Priya") {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "")), 0o600); err != nil {
		t.Fatal(err)
	}
	return vault
}

// mcpCall serves one tool call over stdio against vault, the way a host does,
// and returns the text of the result and whether it was an error.
func mcpCall(t *testing.T, vault, tool string, args map[string]any) (string, bool) {
	t.Helper()
	ix, err := index.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	call, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}` + "\n" + string(call) + "\n")
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(mcpserver.New(ix.DB, nil, vault).Serve(in, pw))
	}()
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var resp struct {
			ID     int `json:"id"`
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &resp) != nil || resp.ID != 2 {
			continue
		}
		go io.Copy(io.Discard, pr)
		if len(resp.Result.Content) == 0 {
			t.Fatalf("tools/call %s returned no content: %s", tool, sc.Text())
		}
		return resp.Result.Content[0].Text, resp.Result.IsError
	}
	t.Fatalf("no reply to tools/call %s: %v", tool, sc.Err())
	return "", false
}

// The point of one service layer is that a surface cannot drift from another:
// #241 was the hand-edit announcement fixed for the CLI while MCP said nothing.
// The same vault asked the same question has to report the same outcome — the
// memories, the queue adoption it did on the way, and what is waiting — word
// for word, whichever door it came in by.
func TestTheCLIAndMCPReportTheSameOutcomeForARecall(t *testing.T) {
	noRuntime(t)
	t.Setenv("LOGOS_EMBED", "off")

	cliVault := recallFixture(t)
	t.Setenv("LOGOS_VAULT", cliVault)
	var cliErr error
	cli := captureStdout(t, func() {
		cliErr = recallCmd([]string{"staging port", "--project", "kestrel"})
	})
	if cliErr != nil {
		t.Fatalf("logos recall: %v", cliErr)
	}

	mcp, isErr := mcpCall(t, recallFixture(t), "recall", map[string]any{"query": "staging port", "project": "kestrel"})
	if isErr {
		t.Fatalf("MCP recall errored: %s", mcp)
	}

	for _, want := range []string{"the kestrel staging port is 9090", "1 proposal rejected", "1 memory is waiting for your review"} {
		if !strings.Contains(mcp, want) {
			t.Fatalf("the fixture does not exercise %q — MCP said:\n%s", want, mcp)
		}
	}
	if strings.TrimSpace(cli) != strings.TrimSpace(mcp) {
		t.Errorf("the two surfaces report different outcomes for one recall\n--- CLI ---\n%s\n--- MCP ---\n%s", cli, mcp)
	}
}

// A memory that reached the cache and not the vault is invariant 4's exact
// case: half an operation succeeded. Both surfaces used to return only the
// error, so the memory's id — the one thing that lets anyone find it, re-save
// it or forget it — was dropped on the way out, along with any credential the
// write had redacted. The failure and what did happen travel together.
func TestAFailedVaultWriteIsInTheOutcomeOnEverySurface(t *testing.T) {
	noRuntime(t)
	t.Setenv("LOGOS_EMBED", "off")
	idLine := regexp.MustCompile(`#\d+`)

	for _, surface := range []string{"cli", "mcp"} {
		t.Run(surface, func(t *testing.T) {
			vault := t.TempDir()
			memDir := filepath.Join(vault, memory.Dir)
			if err := os.MkdirAll(memDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(memDir, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(memDir, 0o700) })
			const fact = "the deploy key is sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA for staging"

			var out string
			var failed bool
			switch surface {
			case "cli":
				t.Setenv("LOGOS_VAULT", vault)
				var err error
				out = captureStdout(t, func() {
					err = memoryCmd([]string{"add", fact, "--project", "kestrel"})
				})
				if err != nil {
					out += "\nerror: " + err.Error()
					failed = true
				}
			case "mcp":
				out, failed = mcpCall(t, vault, "remember", map[string]any{"text": fact, "project": "kestrel"})
			}

			if !failed {
				t.Fatalf("a write the vault refused was reported as a success:\n%s", out)
			}
			// The host shows the badge to the user as "done"; on a write the
			// vault refused it contradicts the error it sits in.
			if strings.Contains(out, "✓") {
				t.Errorf("a failed write carries the success badge:\n%s", out)
			}
			if !strings.Contains(out, "not to the vault") {
				t.Errorf("the vault failure is not said:\n%s", out)
			}
			if !idLine.MatchString(out) {
				t.Errorf("the memory is in the cache and the report does not say which one it is:\n%s", out)
			}
			if !strings.Contains(out, "redacted") {
				t.Errorf("the credential the write redacted went unmentioned once the vault failed:\n%s", out)
			}
		})
	}
}
