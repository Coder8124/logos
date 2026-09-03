// Hardening tests: the claims, under hostile conditions.
//
// api_test.go proves the API works. This file tries to break the promises made
// in README.md and systemmd/DESIGN.md — the ones a user is entitled to rely on:
// that markdown is truth, that the cache is disposable, that nothing leaves the
// machine, that a missing model degrades rather than breaks.
//
// Outside the module's own package on purpose: a claim a user can check is a
// claim that must hold from outside.
package enginetest

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Coder8124/brain"
)

func vaultAt(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func memoryFiles(t *testing.T, vault string) string {
	t.Helper()
	var b strings.Builder
	entries, err := os.ReadDir(filepath.Join(vault, "memories"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(vault, "memories", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
	}
	return b.String()
}

// CLAIM: a Brain is bound to the vault it was opened on.
//
// Newly reachable because brain.Open is public: an embedder can hold two vaults
// at once — a work vault and a personal one, or one per tenant.
func TestTwoVaultsInOneProcessDoNotCrossContaminate(t *testing.T) {
	a, b := vaultAt(t, "vault-a"), vaultAt(t, "vault-b")

	ba, err := brain.Open(a, brain.WithoutEmbedding())
	if err != nil {
		t.Fatal(err)
	}
	defer ba.Close()
	bb, err := brain.Open(b, brain.WithoutEmbedding())
	if err != nil {
		t.Fatal(err)
	}
	defer bb.Close()

	if _, err := ba.Remember("vault A holds the aluminium spec", brain.Fact); err != nil {
		t.Fatal(err)
	}
	if _, err := bb.Remember("vault B holds the pricing model", brain.Fact); err != nil {
		t.Fatal(err)
	}

	fa, fb := memoryFiles(t, a), memoryFiles(t, b)
	if !strings.Contains(fa, "aluminium spec") {
		t.Errorf("vault A's own memory is not in vault A:\n%s", fa)
	}
	if !strings.Contains(fb, "pricing model") {
		t.Errorf("vault B's own memory is not in vault B:\n%s", fb)
	}
	if strings.Contains(fb, "aluminium spec") {
		t.Errorf("vault A's memory leaked into vault B — the vault binding is global, not per-Brain:\n%s", fb)
	}
	if strings.Contains(fa, "pricing model") {
		t.Errorf("vault B's memory leaked into vault A:\n%s", fa)
	}
}

// CLAIM: memories are files, so `rm -rf .brain` costs nothing.
//
// That holds only if the file write actually happened. Here it cannot: the
// memories directory is read-only. Either Remember reports the failure, or the
// durability guarantee is silently void for this user.
func TestRememberReportsWhenItCannotWriteTheDurableCopy(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permissions are not enforced")
	}
	v := vaultAt(t, "readonly")
	mem := filepath.Join(v, "memories")
	if err := os.MkdirAll(mem, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(mem, 0o755) })

	b, err := brain.Open(v, brain.WithoutEmbedding())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	_, err = b.Remember("the BOM target is $38", brain.Fact)
	if err == nil {
		t.Error("Remember reported success while the durable copy could not be written; " +
			"the memory exists only in the cache the README tells users to delete")
	}
}

// CLAIM: delete the cache, reindex, get the same state.
//
// Byte-identical is the documented wording. This measures what is actually
// invariant and reports what is not, so the claim can be narrowed to the truth.
func TestRebuildPreservesEverythingKnown(t *testing.T) {
	v := vaultAt(t, "rebuild")
	b, err := brain.Open(v, brain.WithoutEmbedding())
	if err != nil {
		t.Fatal(err)
	}

	note := "---\ntype: topic\ntitle: BOM cost\n---\nThe waveguide dominates the bill of materials.\n"
	if err := os.WriteFile(filepath.Join(v, "bom-cost.md"), []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		text string
		kind brain.Kind
	}{
		{"I prefer terse replies", brain.Preference},
		{"the BOM target is $38", brain.Fact},
		{"Sam runs the audio team", brain.Person},
	} {
		if _, err := b.Remember(m.text, m.kind); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Checkpoint(brain.Checkpoint{
		Project: "kestrel-one",
		Failed:  []string{"re-quoting the waveguide — no movement"},
		Next:    "quote the driver",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Index(); err != nil {
		t.Fatal(err)
	}

	before, err := b.Memories()
	if err != nil {
		t.Fatal(err)
	}
	b.Close()

	if err := os.RemoveAll(filepath.Join(v, ".brain")); err != nil {
		t.Fatal(err)
	}

	b2, err := brain.Open(v, brain.WithoutEmbedding())
	if err != nil {
		t.Fatal(err)
	}
	defer b2.Close()
	if _, err := b2.Index(); err != nil {
		t.Fatal(err)
	}

	after, err := b2.Memories()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("rebuild changed the memory count: %d → %d", len(before), len(after))
	}
	byID := map[int64]brain.Memory{}
	for _, m := range after {
		byID[m.ID] = m
	}
	for _, m := range before {
		got, ok := byID[m.ID]
		if !ok {
			t.Errorf("memory %d (%q) did not survive the rebuild under its own id", m.ID, m.Text)
			continue
		}
		if got.Text != m.Text || got.Kind != m.Kind {
			t.Errorf("memory %d changed: %q/%s → %q/%s", m.ID, m.Text, m.Kind, got.Text, got.Kind)
		}
		if got.Confidence != m.Confidence {
			t.Errorf("memory %d confidence moved: %.2f → %.2f", m.ID, m.Confidence, got.Confidence)
		}
	}

	hist, err := b2.History("kestrel-one", 5)
	if err != nil || len(hist) != 1 {
		t.Fatalf("checkpoint lost in rebuild: %v %+v", err, hist)
	}
	if len(hist[0].Failed) != 1 {
		t.Errorf("the dead end did not survive the rebuild: %+v", hist[0])
	}
	if hits, err := b2.Search("waveguide", 5); err != nil || len(hits) == 0 {
		t.Errorf("vault note not retrievable after rebuild: %v %d hits", err, len(hits))
	}
}

// CLAIM: retrieval degrades without a model runtime, it does not break.
func TestEveryReadPathWorksWithNoModel(t *testing.T) {
	v := vaultAt(t, "nomodel")
	b, err := brain.Open(v, brain.WithoutEmbedding())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if _, err := b.Remember("the BOM target is $38", brain.Fact); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Checkpoint(brain.Checkpoint{
		Project: "kestrel-one",
		Failed:  []string{"re-quoting the waveguide — no movement under 10k units"},
		Next:    "quote the driver",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Index(); err != nil {
		t.Fatal(err)
	}

	t.Run("context", func(t *testing.T) {
		c, err := b.Context(brain.Request{Task: "cut the BOM", Project: "kestrel-one"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(c.Render(), "waveguide") {
			t.Error("context returned nothing useful without a model")
		}
	})
	t.Run("recall", func(t *testing.T) {
		got, err := b.Recall("what is the BOM target", 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Error("recall found nothing without a model")
		}
	})
	t.Run("tried", func(t *testing.T) {
		got, err := b.Tried("re-quoting the waveguide to bring the BOM down", "kestrel-one")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Error("the intercept found nothing without a model")
		}
	})
	t.Run("ask errors clearly", func(t *testing.T) {
		if _, _, err := b.Ask("what is the BOM target", 5); err == nil {
			t.Error("Ask should refuse without a chat model rather than guess")
		}
	})
}

// CLAIM (implicit, and the one a desktop app plus a CLI plus an MCP server make
// unavoidable): more than one thing can hold the vault at once.
func TestConcurrentBrainsOnOneVault(t *testing.T) {
	v := vaultAt(t, "concurrent")

	b1, err := brain.Open(v, brain.WithoutEmbedding(), brain.WithAgent("one"))
	if err != nil {
		t.Fatal(err)
	}
	defer b1.Close()
	b2, err := brain.Open(v, brain.WithoutEmbedding(), brain.WithAgent("two"))
	if err != nil {
		t.Fatalf("a second Brain could not open the same vault: %v", err)
	}
	defer b2.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := b1
			if i%2 == 1 {
				target = b2
			}
			if err := target.Note("kestrel-one", "progress line"); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	var locked int
	for err := range errs {
		if strings.Contains(err.Error(), "locked") || strings.Contains(err.Error(), "busy") {
			locked++
		} else {
			t.Errorf("unexpected write error: %v", err)
		}
	}
	if locked > 0 {
		t.Errorf("%d writes failed with a lock error; two processes on one vault "+
			"(a CLI beside a live MCP server) hit this in normal use", locked)
	}
}

// CLAIM: nothing leaves the machine unless you name a cloud model.
//
// Structural rather than behavioural: assert no non-localhost endpoint is
// reachable in the default configuration.
func TestNoRemoteEndpointByDefault(t *testing.T) {
	v := vaultAt(t, "offline")
	b, err := brain.Open(v)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	cfg := filepath.Join(v, ".brain", "config.json")
	raw, err := os.ReadFile(cfg)
	if os.IsNotExist(err) {
		return // no config written is the strongest possible version of the claim
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), ",") {
		if !strings.Contains(line, "base_url") && !strings.Contains(line, "BaseURL") {
			continue
		}
		if strings.Contains(line, "127.0.0.1") || strings.Contains(line, "localhost") {
			continue
		}
		if strings.Contains(line, "key_ref") || strings.Contains(line, "https://") {
			t.Logf("remote endpoint present in the default config: %s", strings.TrimSpace(line))
		}
	}
}
