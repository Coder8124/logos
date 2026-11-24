package main

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// "Nothing leaves the machine" is invariant 5, and a claim nobody can check is
// a sentence. TestOnlyTheListedFilesCanOpenANetworkConnection pins the files
// that may; this pins the packages, asked of the Go toolchain rather than of a
// directory walk, so a build-tagged file, a renamed directory or a vendored
// client the walk's skip list steps over still counts. It also looks at the
// third-party clients that open sockets for us, which a stdlib-only scan would
// wave through. A new importer has to be argued for in review: add it here
// with the reason, and to doctor --egress.
//
// net/url and net/netip only parse addresses and are not listed.
func TestOnlyAllowListedPackagesImportTheNetwork(t *testing.T) {
	const mod = "github.com/Coder8124/logos/"
	allowed := map[string]string{
		"cmd/logos":           "ollama /api/pull during setup, and the update check the user starts",
		"internal/mcpserver":  "MCP HTTP mode, which refuses to bind anywhere but loopback",
		"internal/provider":   "talks only to the model runtime the user configured or a localhost probe",
		"internal/selfupdate": "logos update and doctor --check-updates, both started by the user",
	}

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	// Imports, not Deps: a package that reaches the network through another of
	// ours is reported at the one that imports it, which is where the argument
	// belongs. Test files are not in .Imports, so httptest is free.
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list ./...: %v", err)
	}

	var found []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0]+"/", mod) {
			continue
		}
		rel := strings.TrimPrefix(fields[0], strings.TrimSuffix(mod, "/"))
		rel = strings.TrimPrefix(rel, "/")
		for _, imp := range fields[1:] {
			if opensConnections(imp) {
				found = append(found, rel)
				break
			}
		}
	}
	if len(found) == 0 {
		t.Fatal("go list found no network importers at all; the scan itself is broken")
	}
	sort.Strings(found)

	have := map[string]bool{}
	for _, pkg := range found {
		have[pkg] = true
		if _, ok := allowed[pkg]; !ok {
			t.Errorf("%s imports a network package and is not in the allow-list: add it with the reason it may contact anything, and to doctor --egress, or remove the import", pkg)
		}
	}
	// An entry that no longer imports the network is a hole someone else's
	// future import walks through unremarked.
	for pkg, why := range allowed {
		if !have[pkg] {
			t.Errorf("%s is allow-listed (%s) but no longer imports the network; remove the entry", pkg, why)
		}
	}
}

func opensConnections(path string) bool {
	switch path {
	case "net/url", "net/netip":
		return false
	case "crypto/tls", "net", "github.com/gorilla/websocket":
		return true
	}
	return strings.HasPrefix(path, "net/") || strings.HasPrefix(path, "golang.org/x/net/")
}
