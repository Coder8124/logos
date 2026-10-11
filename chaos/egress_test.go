//go:build chaos

package chaos

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// failingProxy refuses every request it is handed and remembers where each was
// going. Go sends any non-loopback request through HTTP_PROXY/HTTPS_PROXY and
// bypasses the proxy for localhost and 127.0.0.1, so the local runtime probes
// still reach this machine and anything else lands here and fails.
//
// It sees only what goes through Go's default transport. A raw net.Dial would
// slip past it; TestOnlyTheListedFilesCanOpenANetworkConnection is what keeps
// the code that could do that to the four files doctor --egress names.
type failingProxy struct {
	mu   sync.Mutex
	seen []string
	srv  *httptest.Server
}

func newFailingProxy(t *testing.T) *failingProxy {
	p := &failingProxy{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.seen = append(p.seen, r.Method+" "+r.Host)
		p.mu.Unlock()
		http.Error(w, "egress refused by the chaos proxy", http.StatusBadGateway)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *failingProxy) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

// env is the environment every command runs in: the proxy, a HOME of its own,
// so no host config or update stamp of the user's is read or written, and a
// PATH with no agent host's CLI on it. doctor runs `claude mcp list`, and
// Claude Code reaches api.anthropic.com on its own when it starts — a request
// doctor --egress names, but not one logos makes, and not one this test can
// stop a host from making.
func (p *failingProxy) env(t *testing.T, vault string, extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k := strings.ToUpper(kv[:strings.IndexByte(kv, '=')])
		if k == "HOME" || k == "PATH" || strings.HasPrefix(k, "LOGOS_") || strings.HasSuffix(k, "_PROXY") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "LOGOS_VAULT="+vault, "LOGOS_AGENT=chaos", "HOME="+t.TempDir(),
		"PATH="+pathWithoutHostCLIs(t), "LOGOS_TEST_NO_BUNDLED_CLIS=1",
		"HTTP_PROXY="+p.srv.URL, "HTTPS_PROXY="+p.srv.URL)
	return append(env, extra...)
}

var hostCLIs = map[string]bool{"claude": true, "codex": true, "opencode": true, "gemini": true, "cursor-agent": true}

// pathWithoutHostCLIs is PATH with every agent host's CLI taken off it, and
// nothing else. Dropping a whole directory that holds one would drop what sits
// beside it too — git and the core tools, when a CLI is installed in /usr/bin —
// and the test would fail for reasons that have nothing to do with egress. So
// such a directory is swapped for one of links to everything else in it.
func pathWithoutHostCLIs(t *testing.T) string {
	var keep []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			keep = append(keep, dir)
			continue
		}
		host := false
		for _, e := range entries {
			host = host || hostCLIs[e.Name()]
		}
		if !host {
			keep = append(keep, dir)
			continue
		}
		links := t.TempDir()
		for _, e := range entries {
			if !hostCLIs[e.Name()] {
				if err := os.Symlink(filepath.Join(dir, e.Name()), filepath.Join(links, e.Name())); err != nil {
					t.Fatal(err)
				}
			}
		}
		keep = append(keep, links)
	}
	return strings.Join(keep, string(filepath.ListSeparator))
}

// The proxy has to be shown to catch something, or a test that finds nothing
// proves nothing: a runtime configured on another host is the one address
// logos is told to contact off this machine, and its probe must land here.
func TestTheEgressProxyCatchesARuntimeOnAnotherHost(t *testing.T) {
	p := newFailingProxy(t)
	vault := newVault(t)
	cmd := exec.Command(bin(t), "doctor", "--verbose")
	cmd.Env = p.env(t, vault, "LOGOS_RUNTIME=http://egress-control.invalid:11434/v1")
	out, _ := cmd.CombinedOutput()
	if len(p.requests()) == 0 {
		t.Fatalf("a runtime on another host was never seen by the proxy, so the proxy cannot be trusted to see anything:\n%s", out)
	}
}

// "Nothing leaves your machine" run as a test: every everyday CLI command and
// every MCP tool, against a proxy that fails anything bound off this machine.
// Each must still work, and the proxy must see nothing.
func TestTheCLIAndMCPSurfaceWorkWithEveryNonLoopbackConnectionRefused(t *testing.T) {
	p := newFailingProxy(t)
	vault := newVault(t)
	// doctor fails a vault other accounts can read, and t.TempDir is 0755.
	if err := os.Chmod(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	env := p.env(t, vault)

	for _, args := range [][]string{
		{"note", "egress", "wired the proxy into the chaos tier"},
		{"tried", "dialling out for a model", "--project", "egress", "--ruled-out", "the proxy refused it"},
		{"checkpoint", "egress", "--task", "prove nothing leaves", "--next", "read the proxy log"},
		{"resume", "egress"},
		{"context", "--project", "egress", "what was ruled out"},
		{"tried", "dialling out for a model", "--project", "egress"},
		{"memory", "add", "--project", "egress", "Decided to keep every call on loopback"},
		{"memory", "log"},
		{"recall", "--project", "egress", "loopback"},
		{"why", "chaos/egress_test.go"},
		{"index"},
		{"sessions"},
		{"show", "egress"},
		{"usage"},
		{"doctor"},
		{"doctor", "--egress"},
	} {
		before := len(p.requests())
		cmd := exec.Command(bin(t), args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("logos %s failed with egress refused: %v\n%s", strings.Join(args, " "), err, out)
		}
		if seen := p.requests()[before:]; len(seen) != 0 {
			t.Errorf("logos %s tried to leave the machine: %v", strings.Join(args, " "), seen)
		}
	}

	before := len(p.requests())
	called := callEveryMCPTool(t, env)
	if called < 10 {
		t.Errorf("only %d MCP tools were called; tools/list must have come back short", called)
	}

	if seen := p.requests()[before:]; len(seen) != 0 {
		t.Errorf("the MCP server tried to leave the machine: %v", seen)
	}
}

// callEveryMCPTool starts `logos mcp serve`, lists its tools, and calls each
// one with its required arguments filled in. A tool may answer with an error
// for an argument that makes no sense; what it may not do is fail to answer.
func callEveryMCPTool(t *testing.T, env []string) int {
	t.Helper()
	cmd := exec.Command(bin(t), "mcp", "serve")
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); _ = cmd.Wait() }()

	lines := make(chan []byte)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(lines)
	}()
	id := 0
	call := func(method string, params any) json.RawMessage {
		id++
		msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if _, err := fmt.Fprintf(stdin, "%s\n", msg); err != nil {
			t.Fatalf("%s: writing to the server: %v", method, err)
		}
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("%s: the server exited", method)
				}
				var resp struct {
					ID     int             `json:"id"`
					Result json.RawMessage `json:"result"`
					Error  json.RawMessage `json:"error"`
				}
				if json.Unmarshal(line, &resp) != nil || resp.ID != id {
					continue // a notification, or log noise
				}
				if resp.Error != nil {
					t.Errorf("%s: %s", method, resp.Error)
				}
				return resp.Result
			case <-time.After(60 * time.Second):
				t.Fatalf("%s: no answer in 60s", method)
			}
		}
	}

	call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "chaos", "version": "0"}})
	fmt.Fprintln(stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Properties map[string]struct {
					Type string `json:"type"`
				} `json:"properties"`
				Required []string `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(call("tools/list", map[string]any{}), &list); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	for _, tool := range list.Tools {
		args := map[string]any{}
		for _, name := range tool.InputSchema.Required {
			switch tool.InputSchema.Properties[name].Type {
			case "array":
				args[name] = []string{"egress"}
			case "integer", "number":
				args[name] = 1
			case "boolean":
				args[name] = false
			default:
				args[name] = "egress"
			}
		}
		call("tools/call", map[string]any{"name": tool.Name, "arguments": args})
	}
	return len(list.Tools)
}
