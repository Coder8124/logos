package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTierRoundTrip(t *testing.T) {
	for _, s := range []string{"T0", "t1", " T2 ", "T3"} {
		tier, err := ParseTier(s)
		if err != nil {
			t.Fatalf("ParseTier(%q): %v", s, err)
		}
		if got, err2 := ParseTier(tier.String()); err2 != nil || got != tier {
			t.Errorf("round trip failed for %q", s)
		}
	}
	if _, err := ParseTier("T9"); err == nil {
		t.Error("unknown tier should error")
	}
}

func TestConfigMergePreservesUnspecifiedTiers(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".brain"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A config naming only T2 must not wipe T0 and T1.
	os.WriteFile(ConfigPath(dir), []byte(`{"tiers":{"T2":{"model":"custom-model"}}}`), 0o600)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tiers["T2"].Model != "custom-model" {
		t.Errorf("T2 = %q, want custom-model", cfg.Tiers["T2"].Model)
	}
	if cfg.Tiers["T0"].Model != "nomic-embed-text" {
		t.Errorf("T0 was clobbered: %q", cfg.Tiers["T0"].Model)
	}
}

func TestMissingConfigIsNotAnError(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("absent config should use defaults, got %v", err)
	}
	if cfg.Tiers["T1"].Model == "" {
		t.Error("defaults not populated")
	}
}

func TestCloudIsOffByDefault(t *testing.T) {
	if Defaults().Tiers["T3"].CloudOK {
		t.Fatal("T3 must default to no egress")
	}
}

func TestSavedConfigIsNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	if err := Defaults().Save(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(ConfigPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config perms = %o, want 600", perm)
	}
}

func TestScanFindsSensitivePatterns(t *testing.T) {
	text := "mail me at sam@example.com or call +1 415 555 0134, key sk-abcdefghijklmnopqrstuvwx, in /Users/pragun/notes"
	found := Scan(text)

	kinds := map[string]bool{}
	for _, f := range found {
		kinds[f.Kind] = true
	}
	for _, want := range []string{"email", "phone", "key", "path"} {
		if !kinds[want] {
			t.Errorf("did not detect %s in %q", want, text)
		}
	}
}

func TestRedactIsStablePerValue(t *testing.T) {
	text := "sam@example.com wrote to sam@example.com and to ana@example.com"
	out := Redact(text, Scan(text))

	if strings.Contains(out, "@example.com") {
		t.Errorf("emails survived redaction: %q", out)
	}
	// The same address twice must map to the same placeholder, or the model
	// reads one person as two.
	if strings.Count(out, "[EMAIL_1]") != 2 {
		t.Errorf("placeholder not stable: %q", out)
	}
	if !strings.Contains(out, "[EMAIL_2]") {
		t.Errorf("distinct addresses should get distinct placeholders: %q", out)
	}
}

func TestPreviewReportsAndRedacts(t *testing.T) {
	text := "contact sam@example.com"
	p := Preview(text, Scan(text), 1000)

	if strings.Contains(p, "sam@example.com") {
		t.Error("preview must show the redacted payload, not the raw one")
	}
	if !strings.Contains(p, "email") {
		t.Error("preview should summarise what was found")
	}
}

func TestPreviewTruncatesLongPayloads(t *testing.T) {
	p := Preview(strings.Repeat("x", 5000), nil, 100)
	if !strings.Contains(p, "truncated") {
		t.Error("long payloads should be truncated in the preview")
	}
}

// A generation tier whose model is not installed used to descend all the way
// into T0, which is the embedding tier. The distil call that followed handed
// nomic-embed-text a chat prompt and surfaced as `Ollama returned 400 Bad
// Request` from inside a rollup, instead of saying that no generation model is
// installed.
func TestAGenerationTierDoesNotDegradeIntoTheEmbeddingModel(t *testing.T) {
	r := &Router{
		cfg: &Config{Tiers: map[string]TierConfig{
			"T0": {Model: "nomic-embed-text"},
			"T1": {Model: "not-installed"},
			"T2": {Model: "also-not-installed"},
		}},
		models:   map[string]bool{"nomic-embed-text": true},
		caps:     map[string]Capability{},
		resolved: map[Tier]string{},
	}

	if m, err := r.Model(T2); err == nil {
		t.Errorf("T2 resolved to %q; with no generation model installed it must fail, not hand back an embedding model", m)
	}

	// Asking for the embedding tier itself still resolves it — only the descent
	// into it from above is wrong.
	if m, err := r.Model(T0); err != nil || m != "nomic-embed-text" {
		t.Errorf("Model(T0) = %q, %v; want the configured embedding model", m, err)
	}
}
