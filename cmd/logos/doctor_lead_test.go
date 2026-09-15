package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/provider"
)

// On a healthy install doctor printed fifty lines, most of them a model
// inventory and routing tiers no continuity tool uses, and the three answers a
// coding-agent user came for were scattered among them.
func TestDoctorLeadsWithTheVaultHostsAndCheckpointAndHidesTheModelsUnlessAsked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("LOGOS_EMBED", "")
	vault := filepath.Join(home, "vault")
	if err := os.MkdirAll(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGOS_VAULT", vault)
	emptyRuntime(t, "Ollama")

	out := captureStdout(t, func() { doctor(false, false) })

	rows := regexp.MustCompile(`(?m)^  (\S.*?)\s{2,}(ok|to do|FAILED|unchecked)$`).FindAllStringSubmatch(out, 3)
	var lead []string
	for _, r := range rows {
		lead = append(lead, strings.TrimSpace(r[1]))
	}
	if strings.Join(lead, ",") != "vault,agent hosts,continuity" {
		t.Errorf("doctor leads with %v, want vault, agent hosts, continuity:\n%s", lead, out)
	}
	for _, hidden := range []string{"─── runtimes", "─── tiers", "web bridge:"} {
		if strings.Contains(out, hidden) {
			t.Errorf("doctor without --verbose printed %q:\n%s", hidden, out)
		}
	}
	if !strings.Contains(out, "--verbose") {
		t.Errorf("doctor did not say how to see the runtimes:\n%s", out)
	}

	verbose := captureStdout(t, func() { doctor(false, true) })
	for _, shown := range []string{"─── runtimes", "─── tiers", "web bridge:"} {
		if !strings.Contains(verbose, shown) {
			t.Errorf("doctor --verbose did not print %q:\n%s", shown, verbose)
		}
	}
}

// With no runtime, setup told a coding-agent user to install Ollama, which
// none of the continuity tools need.
func TestSetupWithNoRuntimeDoesNotRecommendInstallingOne(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	old := provider.LocalEndpoints
	provider.LocalEndpoints = []provider.LocalEndpoint{{Name: "Ollama", URL: url + "/v1"}}
	t.Cleanup(func() { provider.LocalEndpoints = old })

	if out := captureStdout(t, func() { checkRuntime(true, false) }); strings.Contains(out, "Ollama") || strings.Contains(out, "runtime") {
		t.Errorf("setup talked about a runtime nobody has:\n%s", out)
	}
}
