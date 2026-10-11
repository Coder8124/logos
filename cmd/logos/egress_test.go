package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/selfupdate"
)

// The claim is "nothing leaves your machine", and `logos doctor --egress` is
// how a user checks it without reading the source. A list that omits an
// address is worse than no list, so it names each one the binary can contact
// and says, in so many words, that there are no others.
func TestDoctorEgressNamesEveryAddressAndSaysThereAreNoOthers(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_RUNTIME", "")
	t.Setenv("LOGOS_PLUGIN_AUTOUPDATE", "off")

	out := captureStdout(t, func() {
		if err := doctorEgress(); err != nil {
			t.Fatal(err)
		}
	})
	want := []string{selfupdate.ReleasesFrom, "githubusercontent.com", "claude plugin", "/api/pull", "127.0.0.1", "no others"}
	for _, ep := range provider.LocalEndpoints {
		want = append(want, ep.URL)
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("doctor --egress does not name %q:\n%s", w, out)
		}
	}
	if !strings.Contains(out, "cloud tier") || !strings.Contains(out, "off") {
		t.Errorf("doctor --egress does not say the cloud tier is off:\n%s", out)
	}
}

// doctor and setup run each host's own CLI to read its registration, and
// Claude Code reaches api.anthropic.com every time it starts. That is not
// logos's request, but it is one running logos causes, and a list that left it
// out was caught by the chaos-tier proxy, not by anyone reading the list.
func TestDoctorEgressNamesTheHostCLIsItRunsAndThatTheyMakeTheirOwnRequests(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	out := captureStdout(t, func() {
		if err := doctorEgress(); err != nil {
			t.Fatal(err)
		}
	})
	for _, w := range []string{"claude mcp list", "their own requests"} {
		if !strings.Contains(out, w) {
			t.Errorf("doctor --egress does not say %q:\n%s", w, out)
		}
	}
}

// A runtime on another machine is the one configured address that does leave
// it, and the list says so rather than filing it with the localhost probes.
func TestDoctorEgressFlagsAConfiguredRuntimeOnAnotherHost(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_RUNTIME", "http://gpu-box.lan:11434/v1")

	out := captureStdout(t, func() {
		if err := doctorEgress(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "gpu-box.lan") || !strings.Contains(out, "not this machine") {
		t.Errorf("doctor --egress did not flag a runtime on another host:\n%s", out)
	}
	if strings.Contains(out, provider.LocalEndpoints[0].URL) {
		t.Errorf("doctor --egress lists the localhost probes, which LOGOS_RUNTIME replaces:\n%s", out)
	}
}

// doctor --egress is a hand-kept list, so it drifts the day someone adds an
// HTTP client and does not add a line. This pins the packages that may open a
// connection; a new one fails here until the list, and this test, name it.
// net/url and net/netip only parse.
func TestOnlyTheListedFilesCanOpenANetworkConnection(t *testing.T) {
	allowed := map[string]bool{
		"cmd/logos/setup_models.go":         true, // Ollama /api/pull, during setup
		"internal/provider/provider.go":     true, // the model runtime
		"internal/mcpserver/http.go":        true, // serve-http, listens on loopback only
		"internal/selfupdate/selfupdate.go": true, // logos update
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "personal", "chaos", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if (p == "net" || strings.HasPrefix(p, "net/") || p == "crypto/tls") && p != "net/url" && p != "net/netip" {
				rel, _ := filepath.Rel(root, path)
				found = append(found, filepath.ToSlash(rel))
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	for _, f := range found {
		if !allowed[f] {
			t.Errorf("%s can open a network connection and doctor --egress does not account for it", f)
		}
	}
	if len(found) != len(allowed) {
		t.Errorf("network-capable files = %v, want exactly %d", found, len(allowed))
	}
}
