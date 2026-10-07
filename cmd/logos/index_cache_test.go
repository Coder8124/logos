package main

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/dream"
	"github.com/Coder8124/logos/internal/memory"
)

// Every table the code creates is in exactly one of these two lists.
//
// "Delete the index, run logos index, lose nothing" broke four times the same
// way — memories, working notes, checkpoints, the review queue — each time a
// table that held something the user wrote and that nothing rebuilt from
// markdown, and each time silently. Each fix came with a test for that one
// table, which says nothing about the next one. These lists are the question
// CLAUDE.md asks of a new table, asked by the build: a table in neither fails
// TestEveryTableTheCodeCreatesIsRebuiltOrDeclaredACache until someone says
// which it is, and a table declared rebuilt is checked row for row below.
//
// Each rebuilt table maps to the query for what the vault holds of it. Most are
// the whole row; the exceptions say what they leave out and why.
var rebuiltTables = map[string]string{
	"notes":   "SELECT * FROM notes",
	"aliases": "SELECT * FROM aliases",
	"edges":   "SELECT * FROM edges",
	// vec is the embedding, a cache of the text beside it.
	"memories": `SELECT id, text, kind, salience, confidence, project, source, agent, created,
	             last_used, uses, fingerprint, superseded, superseded_by, quarantined, pin, unflushed
	             FROM memories`,
	"memory_log": "SELECT * FROM memory_log",
	// A closed session is bookkeeping for its checkpoint file, which is the
	// record and is read from markdown; an open one is re-opened around its
	// working notes under an id taken from the clock. What the vault holds is
	// which project has work open, and the notes themselves.
	"sessions": "SELECT project, agent, task FROM sessions WHERE ended = 0",
	"session_notes": `SELECT s.project, s.agent, n.text, n.ts, n.unflushed
	                  FROM session_notes n JOIN sessions s ON s.id = n.session`,
	"commitments":    "SELECT * FROM commitments",
	"dream_insights": "SELECT * FROM dream_insights",
}

// cacheOnlyTables holds what a rebuild may lose, each with the reason losing it
// costs the user nothing they wrote.
var cacheOnlyTables = map[string]string{
	"embeddings":     "vectors of note text, recomputed by the next index that has a model",
	"ruling_vectors": "vectors of ruled-out text, recomputed the next time a ruling is matched",
	"notes_fts":      "the full-text index over notes, refilled by Sync from the same files",
	"meta":           "last_sync, which a rebuild is itself the new value of",
	"replay_state":   "a read cursor, cache-only on purpose — see internal/replay/state.go",
	"ruling_files":   "files named in checkpoints' Failed lists, re-read from sessions/ by the edit hook on its next run",
	"ruling_shown":   "which rulings the edit hook already showed this session; losing it shows one once more",
}

// createdTables finds every table the code can create by reading the source,
// not by opening an index: some tables are created lazily by the command that
// first needs them, and an index that never ran that command would let a new
// one through.
func createdTables(t *testing.T) map[string]string {
	t.Helper()
	create := regexp.MustCompile(`CREATE (?:VIRTUAL )?TABLE (?:IF NOT EXISTS )?(\w+)\s*(?:\(|USING)`)
	out := map[string]string{}
	for _, root := range []string{"../../internal", "."} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range create.FindAllStringSubmatch(string(src), -1) {
				out[m[1]] = path
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(out) == 0 {
		t.Fatal("found no CREATE TABLE in the source — the scan is broken, not the schema")
	}
	return out
}

func TestEveryTableTheCodeCreatesIsRebuiltOrDeclaredACache(t *testing.T) {
	created := createdTables(t)
	for name := range rebuiltTables {
		if _, ok := cacheOnlyTables[name]; ok {
			t.Errorf("%s is declared both rebuilt and cache-only", name)
		}
	}
	for name, where := range created {
		_, rebuilt := rebuiltTables[name]
		if _, cache := cacheOnlyTables[name]; !rebuilt && !cache {
			t.Errorf("%s creates table %s, which is neither rebuilt from the vault nor declared a cache — "+
				"if it holds anything a user wrote, it needs a markdown home and an import before it needs a writer", where, name)
		}
	}
	// A stale entry would let a renamed table slip past both lists.
	for name := range rebuiltTables {
		if _, ok := created[name]; !ok {
			t.Errorf("%s is declared rebuilt but nothing creates it any more", name)
		}
	}
	for name := range cacheOnlyTables {
		if _, ok := created[name]; !ok {
			t.Errorf("%s is declared cache-only but nothing creates it any more", name)
		}
	}
}

// One vault with something in every rebuilt table, written through the same
// commands a user runs, then the index deleted and rebuilt. Each table must hold
// the same rows afterwards. A table that comes back empty is the bug this suite
// exists for; one that comes back different — a new id, a reset status — is the
// quieter version of it.
func TestEveryRebuiltTableComesBackRowForRowAfterDeletingTheIndex(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_EMBED", "off")
	t.Setenv("LOGOS_PROJECT", "kestrel")

	note := "---\naliases: [the waveguide]\n---\n# Waveguide\n\nCosted in [[bom]].\n"
	if err := os.WriteFile(filepath.Join(vaultDir, "waveguide.md"), []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vaultDir, "bom.md"), []byte("# BOM\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	captureStdout(t, func() {
		for _, fact := range []string{"the waveguide costs 4.20 dollars per unit", "the BOM is owned by procurement"} {
			if err := memoryCmd([]string{"add", "--project", "kestrel", fact}); err != nil {
				t.Fatalf("memory add: %v", err)
			}
		}
		if err := runCheckpoint([]string{"--task", "quote the waveguide",
			"--decided", "extruded frames, because the mould costs too much",
			"--failed", "casting the frame — the mould costs more than the run"}); err != nil {
			t.Fatalf("checkpoint: %v", err)
		}
		if err := runNote([]string{"kestrel", "priced the extruded option"}); err != nil {
			t.Fatalf("note: %v", err)
		}
		for _, loop := range []string{"send the quote to procurement", "ask about lead time"} {
			if err := commitmentCmd([]string{"add", loop}); err != nil {
				t.Fatalf("loop add: %v", err)
			}
		}
		if err := commitmentCmd([]string{"done", "1"}); err != nil {
			t.Fatalf("loop done: %v", err)
		}
	})

	// A proposal and an insight have no model-free command that makes one, so
	// they go in through the same calls the MCP server and the dream pass use.
	ix, err := openIndex()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Store(ix.DB, nil, "", &memory.Memory{
		Text: "procurement wants quotes in euros", Kind: memory.Fact, Source: "mcp", Agent: "claude-code", Quarantined: true,
	}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if err := dream.InitQueue(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := dream.Enqueue(ix.DB, &dream.Insight{
		Kind: dream.Connection, Text: "the BOM owner is who the quote goes to",
		EndpointA: 1, EndpointB: 2, Conf: 0.6, Model: "test",
	}); err != nil {
		t.Fatalf("insight: %v", err)
	}
	ix.Close()

	snapshot := func() map[string][]string {
		captureStdout(t, func() {
			if err := runIndex(false); err != nil {
				t.Fatal(err)
			}
		})
		ix, err := openIndex()
		if err != nil {
			t.Fatal(err)
		}
		defer ix.Close()
		out := map[string][]string{}
		for name, query := range rebuiltTables {
			out[name] = rows(t, ix.DB, query)
		}
		return out
	}

	before := snapshot()
	for name := range rebuiltTables {
		if len(before[name]) == 0 {
			t.Errorf("%s is empty before the rebuild, so this test proves nothing about it — populate it above", name)
		}
	}
	if err := os.RemoveAll(filepath.Join(vaultDir, ".logos")); err != nil {
		t.Fatal(err)
	}
	after := snapshot()

	for name := range rebuiltTables {
		b, a := strings.Join(before[name], "\n"), strings.Join(after[name], "\n")
		if a != b {
			t.Errorf("%s changed when the index was rebuilt from the vault:\nbefore:\n%s\nafter:\n%s", name, b, a)
		}
	}
}

// rows renders every row a query returns as one line, sorted, so the comparison
// is about content and not the order a rebuild happened to insert it in.
func rows(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rs, err := db.Query(query)
	if err != nil && strings.Contains(err.Error(), "no such table") {
		// A rebuild that never recreated the table lost every row in it; let
		// the comparison say so, with the rows it lost.
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	cols, err := rs.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			parts[i] = fmt.Sprintf("%s=%v", cols[i], v)
		}
		out = append(out, strings.Join(parts, " "))
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}
