package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

// claudeMemFixture writes a store shaped like claude-mem's: the observations
// columns its first schema had, plus merged_into_project from a later
// migration, so the importer is tested against a real-shaped table rather than
// one it was written to expect.
func claudeMemFixture(t *testing.T, rows [][4]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-mem.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE observations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		memory_session_id TEXT NOT NULL,
		project TEXT NOT NULL,
		text TEXT,
		type TEXT NOT NULL,
		title TEXT,
		subtitle TEXT,
		facts TEXT,
		narrative TEXT,
		created_at TEXT NOT NULL,
		created_at_epoch INTEGER NOT NULL,
		merged_into_project TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i, r := range rows { // project, title, narrative, merged_into_project
		var merged any
		if r[3] != "" {
			merged = r[3]
		}
		if _, err := db.Exec(`INSERT INTO observations
			(memory_session_id, project, type, title, narrative, created_at, created_at_epoch, merged_into_project)
			VALUES ('s1', ?, 'discovery', ?, ?, '2026-10-01', ?, ?)`, r[0], r[1], r[2], 1000+i, merged); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func importFrom(t *testing.T, db string, extra ...string) string {
	t.Helper()
	return captureStdout(t, func() {
		if err := runImport(append([]string{"--from", "claude-mem", "--db", db, "--yes"}, extra...)); err != nil {
			t.Fatalf("import: %v", err)
		}
	})
}

func importedMemories(t *testing.T) []memory.Memory {
	t.Helper()
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	all, err := memory.All(ix.DB)
	if err != nil {
		t.Fatal(err)
	}
	var out []memory.Memory
	for _, m := range all {
		if m.Source == claudeMemSource {
			out = append(out, m)
		}
	}
	return out
}

func TestImportingTwiceAddsNothingTheSecondTime(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_EMBED", "off")
	db := claudeMemFixture(t, [][4]string{
		{"kestrel", "Waveguide supplier quotes 4.20 per unit", "", ""},
		{"kestrel", "Staging runs on port 9090", "", ""},
	})

	first := importFrom(t, db)
	if !strings.Contains(first, "Imported 2, skipped 0") {
		t.Errorf("first import did not report two new memories:\n%s", first)
	}
	second := importFrom(t, db)
	if !strings.Contains(second, "Imported 0, skipped 2 (already present)") {
		t.Errorf("second import did not report everything as already present:\n%s", second)
	}
	if got := len(importedMemories(t)); got != 2 {
		t.Errorf("two imports of the same two observations left %d memories", got)
	}
}

func TestAnImportedSecretIsRedactedAndCounted(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_EMBED", "off")
	const key = "sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGh"
	db := claudeMemFixture(t, [][4]string{
		{"kestrel", "Deploy uses the key " + key + " from the vault", "", ""},
		{"kestrel", key, "", ""},
	})

	out := importFrom(t, db)
	if !strings.Contains(out, "Imported 1") || !strings.Contains(out, "refused 1 (nothing but a secret)") {
		t.Errorf("a masked memory and a secret-only one were not reported as one imported, one refused:\n%s", out)
	}
	if !strings.Contains(out, "1 had a secret-shaped token masked") {
		t.Errorf("the masking was not announced:\n%s", out)
	}
	got := importedMemories(t)
	if len(got) != 1 {
		t.Fatalf("want the one masked memory, got %d", len(got))
	}
	if strings.Contains(got[0].Text, key) {
		t.Errorf("the key reached the store: %q", got[0].Text)
	}
}

// The vault is truth: an import that lived only in the index would be lost to
// the next `rm -rf .logos && logos index`, silently.
func TestImportWritesTheVaultBeforeTheIndex(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_EMBED", "off")
	db := claudeMemFixture(t, [][4]string{
		{"kestrel", "Waveguide supplier quotes 4.20 per unit", "", ""},
	})
	importFrom(t, db)

	raw, err := os.ReadFile(filepath.Join(vaultDir, memory.Dir, "fact.md"))
	if err != nil || !strings.Contains(string(raw), "Waveguide supplier quotes") {
		t.Fatalf("the imported memory is not in memories/fact.md (err %v):\n%s", err, raw)
	}
	if err := os.RemoveAll(filepath.Join(vaultDir, ".logos")); err != nil {
		t.Fatal(err)
	}
	if err := runIndex(false); err != nil {
		t.Fatal(err)
	}
	got := importedMemories(t)
	if len(got) != 1 || got[0].Project != "kestrel" {
		t.Errorf("after rebuilding the index the import came back as %+v", got)
	}
}

// claude-mem records a merge by naming the surviving project on the old rows;
// importing under the old name would split one project in two here.
func TestAMergedClaudeMemProjectImportsUnderItsNewName(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_EMBED", "off")
	db := claudeMemFixture(t, [][4]string{
		{"kestrel-old", "Waveguide supplier quotes 4.20 per unit", "", "kestrel"},
		{"osprey", "Osprey builds with make", "", ""},
	})
	importFrom(t, db, "--project", "kestrel")

	got := importedMemories(t)
	if len(got) != 1 || got[0].Project != "kestrel" {
		t.Errorf("--project kestrel imported %+v, want only the merged row under kestrel", got)
	}
}

func TestADryRunImportWritesNothing(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_EMBED", "off")
	db := claudeMemFixture(t, [][4]string{
		{"kestrel", "", "A narrative with no title still becomes a memory", ""},
	})
	out := captureStdout(t, func() {
		if err := runImport([]string{"--from", "claude-mem", "--db", db, "--dry-run"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "1 memories would be written. Nothing was.") || !strings.Contains(out, "A narrative with no title") {
		t.Errorf("dry run did not show what it would write:\n%s", out)
	}
	if got := importedMemories(t); len(got) != 0 {
		t.Errorf("a dry run wrote %d memories", len(got))
	}
}

// The path went into the SQLite URI as typed, where `?` starts the query and
// `#` the fragment, so a store in a directory with either in its name passed
// the existence check and then failed to open.
func TestAClaudeMemStoreImportsFromADirectoryNamedWithURICharacters(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_EMBED", "off")
	made := claudeMemFixture(t, [][4]string{{"kestrel", "Staging runs on port 9090", "", ""}})
	odd := filepath.Join(t.TempDir(), "mem?v=2#old%20copy")
	if err := os.MkdirAll(odd, 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(odd, "claude-mem.db")
	if err := os.Rename(made, db); err != nil {
		t.Fatal(err)
	}

	if out := importFrom(t, db); !strings.Contains(out, "Imported 1") {
		t.Errorf("the store did not import:\n%s", out)
	}
}
