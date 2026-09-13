package main

import "testing"

// LOGOS_EMBED=off is the shell equivalent of logos.WithoutEmbedding(): the docs
// promise "no model runtime is required", and the way a shell asks for that is
// an env var. Passed straight through, "off" reached Ollama as a model name and
// came back a 404.
func TestEmbedModelOffDisablesEmbeddings(t *testing.T) {
	for _, v := range []string{"off", "none", "no", "false", "disabled", "OFF", " off "} {
		t.Setenv("LOGOS_EMBED", v)
		if m, ok := embedModel(); ok || m != "" {
			t.Errorf("LOGOS_EMBED=%q should disable embeddings, got (%q, %v)", v, m, ok)
		}
	}
}

func TestEmbedModelDefaultsAndPassesAModelThrough(t *testing.T) {
	t.Setenv("LOGOS_EMBED", "")
	if m, ok := embedModel(); !ok || m != defaultEmbedModel {
		t.Errorf("unset should give the default, got (%q, %v)", m, ok)
	}
	t.Setenv("LOGOS_EMBED", "mxbai-embed-large")
	if m, ok := embedModel(); !ok || m != "mxbai-embed-large" {
		t.Errorf("an explicit model should pass through, got (%q, %v)", m, ok)
	}
}

// Discovery only probes localhost. LOGOS_RUNTIME is the only way to reach a
// runtime on another host, and the only way to test the no-runtime path on a
// machine that has Ollama up — so it must not touch the network to resolve.
func TestLogosRuntimeOverridesDiscovery(t *testing.T) {
	t.Setenv("LOGOS_RUNTIME", "http://192.0.2.1:1234/v1")
	t.Setenv("LOGOS_RUNTIME_KEY", "sk-test")
	p, err := findProvider()
	if err != nil {
		t.Fatalf("LOGOS_RUNTIME should resolve without a probe: %v", err)
	}
	if p.BaseURL != "http://192.0.2.1:1234/v1" {
		t.Errorf("want the configured base URL, got %q", p.BaseURL)
	}
}
