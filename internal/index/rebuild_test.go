package index

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/dream"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/replay"
	"github.com/Coder8124/logos/internal/secretary"
	"github.com/Coder8124/logos/internal/session"
)

// The name must be followed by a column list or USING, so prose such as a
// comment saying "CREATE TABLE IF NOT EXISTS is a no-op" is not read as a table.
var createTable = regexp.MustCompile(`CREATE (?:VIRTUAL )?TABLE IF NOT EXISTS (\w+)\s*(?:\(|USING\b)`)

// definedTables reads every CREATE TABLE in the module's non-test source. The
// source, not a freshly opened index, because the deadend and replay tables are
// created lazily by their first reader — an index that has only been opened
// never has them, and a check against sqlite_master alone would pass a new
// lazy table that nothing rebuilds.
func definedTables(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..")
	found := map[string]string{}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range createTable.FindAllSubmatch(src, -1) {
				found[string(m[1])] = path
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(found) == 0 {
		t.Fatal("found no CREATE TABLE in the source at all; the scan is looking in the wrong place")
	}
	return found
}

func TestEveryIndexTableNamesWhatRebuildsItFromTheVault(t *testing.T) {
	defined := definedTables(t)

	for table, path := range defined {
		if strings.TrimSpace(RebuildPaths[table]) == "" {
			t.Errorf("%s creates table %q, and RebuildPaths does not say what rebuilds it from the vault or why losing it is safe", path, table)
		}
	}

	// Some tables could be created by SQL the scan does not recognise; what a
	// database actually holds once every store has created its tables is the
	// second witness. Open alone is not enough: each store creates its own
	// tables on first use. (ruling_vectors is created only inside an embedding
	// check, so for it the source scan is the only witness.)
	dir := t.TempDir()
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	for _, init := range []func(*sql.DB) error{memory.Init, session.Init, secretary.Init, dream.InitQueue, replay.Init} {
		if err := init(ix.DB); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := deadend.AtPath(ix.DB, dir, "p", "x.go"); err != nil {
		t.Fatal(err)
	}
	rows, err := ix.DB.Query(`SELECT name FROM sqlite_master WHERE type = 'table'
		AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'notes_fts_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if _, ok := RebuildPaths[name]; !ok {
			t.Errorf("an opened index holds table %q, and RebuildPaths does not name it", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	// A stale entry is a promise about a table that no longer exists, which
	// makes the list read as more complete than it is.
	var stale []string
	for table := range RebuildPaths {
		if _, ok := defined[table]; !ok {
			stale = append(stale, table)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("RebuildPaths names tables nothing creates any more: %v", stale)
	}
}
