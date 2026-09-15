package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// Claude Code caches a plugin server that fails to start and skips it for 15
// minutes, in every session on the machine, and a session that started inside
// that window never gets it back. `mcp serve` exited before the handshake on a
// vault it could not open — an explicit LOGOS_VAULT with a typo, a drive not
// mounted — so one bad start switched Logos off for a quarter of an hour, with
// the cause only in a log. A server that answers the handshake and returns the
// cause from every tool call is read by the model at once instead.
func TestMCPServeWithAVaultItCannotOpenStillAnswersTheHandshakeAndSaysWhy(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-vault")
	t.Setenv("LOGOS_VAULT", missing)

	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"resume","arguments":{"project":"kestrel-one"}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(in), &out); err != nil {
		t.Fatalf("mcp serve exited with %v before answering anything", err)
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
	init, ok := replies[1]["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize was not answered:\n%s", out.String())
	}
	if !strings.Contains(init["instructions"].(string), missing) {
		t.Errorf("the instructions do not name the vault it could not open: %v", init["instructions"])
	}
	if _, ok := replies[2]["result"]; !ok {
		t.Errorf("tools/list was not answered:\n%s", out.String())
	}
	call, _ := replies[3]["result"].(map[string]any)
	if call["isError"] != true || !strings.Contains(out.String(), "vault not found") {
		t.Errorf("a tool call did not come back as an error naming the cause:\n%s", out.String())
	}
}
