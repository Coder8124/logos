package main

import "testing"

// BRAIN_EMBED=off is the shell equivalent of brain.WithoutEmbedding(): the docs
// promise "no model runtime is required", and the way a shell asks for that is
// an env var. Passed straight through, "off" reached Ollama as a model name and
// came back a 404.
func TestEmbedModelOffDisablesEmbeddings(t *testing.T) {
	for _, v := range []string{"off", "none", "no", "false", "disabled", "OFF", " off "} {
		t.Setenv("BRAIN_EMBED", v)
		if m, ok := embedModel(); ok || m != "" {
			t.Errorf("BRAIN_EMBED=%q should disable embeddings, got (%q, %v)", v, m, ok)
		}
	}
}

func TestEmbedModelDefaultsAndPassesAModelThrough(t *testing.T) {
	t.Setenv("BRAIN_EMBED", "")
	if m, ok := embedModel(); !ok || m != defaultEmbedModel {
		t.Errorf("unset should give the default, got (%q, %v)", m, ok)
	}
	t.Setenv("BRAIN_EMBED", "mxbai-embed-large")
	if m, ok := embedModel(); !ok || m != "mxbai-embed-large" {
		t.Errorf("an explicit model should pass through, got (%q, %v)", m, ok)
	}
}

// Discovery only probes localhost. BRAIN_RUNTIME is the only way to reach a
// runtime on another host, and the only way to test the no-runtime path on a
// machine that has Ollama up — so it must not touch the network to resolve.
func TestBrainRuntimeOverridesDiscovery(t *testing.T) {
	t.Setenv("BRAIN_RUNTIME", "http://192.0.2.1:1234/v1")
	t.Setenv("BRAIN_RUNTIME_KEY", "sk-test")
	p, err := findProvider()
	if err != nil {
		t.Fatalf("BRAIN_RUNTIME should resolve without a probe: %v", err)
	}
	if p.BaseURL != "http://192.0.2.1:1234/v1" {
		t.Errorf("want the configured base URL, got %q", p.BaseURL)
	}
}
