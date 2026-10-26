package main

import (
	"database/sql"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/ops"
)

// durableColumns is what each table must hold the same after .logos is
// deleted and rebuilt: the columns a reader acts on. Row ids are left out —
// a rebuild is free to number rows differently, and every reader that crosses
// tables goes through uid or text — as are use counts and other bookkeeping
// RebuildPaths calls cache. A table RebuildPaths marks "cache:" is not
// compared at all.
var durableColumns = map[string]string{
	"notes":          "slug, path, title, kind, body, hash",
	"aliases":        "slug, alias",
	"edges":          "src_slug, pred, obj, src",
	"notes_fts":      "slug, title, body",
	"memories":       "uid, kind, text, project, source, agent, quarantined, superseded, supersedes, pin",
	"memory_log":     "event, detail, project",
	"commitments":    "text, who, status",
	"dream_insights": "kind, text, status",
	"sessions":       "project, agent, ended",
	"session_notes":  "text, ts",
}

// durableWhere narrows a comparison to the rows a rebuild promises to restore.
// An ended session's rows are not among them: its checkpoint file is the
// record, every reader of the live tables filters ended = 0, and ImportNotes
// restores only the sessions still open. Nor is a superseded memory: its file
// is deleted when it is replaced, the replacement names it by uid, and log.md
// holds the history (see Memory.Supersedes).
//
// An open session's id, start time and task are left out of the columns above
// for a narrower reason: uncommitted.md records each note's text, time and
// agent and nothing about the session, so a rebuild opens a fresh one around
// them. That is safe — the id names no file until a checkpoint, which creates
// its file exclusively — but it is not the same row.
var durableWhere = map[string]string{
	"memories":      "WHERE superseded = 0",
	"sessions":      "WHERE ended = 0",
	"session_notes": "WHERE session IN (SELECT id FROM sessions WHERE ended = 0)",
}

// The promise every document makes is "delete the index, run logos index,
// lose nothing", and it has broken four times, each for one writer nobody
// thought to test. This drives a random mix of the real writers, then checks
// that a rebuild from the vault alone holds exactly what the writers left in
// the cache — for every durable table RebuildPaths lists, not only the ones a
// test happens to look at.
func TestReindexingARandomVaultGivesTheSameIndex(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3, 4, 5} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Logf("seed %d", seed)
			vaultDir := t.TempDir()
			t.Setenv("LOGOS_VAULT", vaultDir)
			t.Setenv("LOGOS_EMBED", "off")
			t.Setenv("LOGOS_PROJECT", "propproj")
			r := rand.New(rand.NewPCG(seed, seed))

			populateRandomVault(t, vaultDir, r)
			// One pass first, so the comparison is cache-then-rebuild rather
			// than "rows the writers made" against "rows a pass makes": the
			// note tables are only ever filled by a pass.
			captureStdout(t, func() {
				if err := runIndex(false); err != nil {
					t.Fatalf("index: %v", err)
				}
			})
			before := dumpDurable(t, vaultDir)

			if err := os.RemoveAll(filepath.Join(vaultDir, ".logos")); err != nil {
				t.Fatal(err)
			}
			captureStdout(t, func() {
				if err := runIndex(false); err != nil {
					t.Fatalf("reindex after deleting the cache: %v", err)
				}
			})
			after := dumpDurable(t, vaultDir)

			for table := range durableColumns {
				if before[table] != after[table] {
					t.Errorf("seed %d: table %s differs after deleting .logos and reindexing\nbefore:\n%s\nafter:\n%s",
						seed, table, before[table], after[table])
				}
			}
		})
	}
}

func TestDurableColumnsCoverEveryTableThatIsNotACache(t *testing.T) {
	for table, path := range index.RebuildPaths {
		_, compared := durableColumns[table]
		cache := strings.HasPrefix(path, "cache:")
		if cache && compared {
			t.Errorf("%s is marked cache in RebuildPaths but compared by the reindex property test", table)
		}
		if !cache && !compared {
			t.Errorf("%s is rebuilt from the vault (%s) but the reindex property test never compares it", table, path)
		}
	}
}

var (
	propKinds = []memory.Kind{memory.Fact, memory.Preference, memory.Person, memory.Context}
	propWords = []string{"waveguide", "harrier", "kestrel", "lattice", "ferrite", "quorum", "basalt", "tundra", "marlin", "osprey"}
)

func propPhrase(r *rand.Rand, n int) string {
	w := make([]string, n)
	for i := range w {
		w[i] = propWords[r.IntN(len(propWords))]
	}
	return strings.Join(w, " ")
}

// populateRandomVault writes through the same paths the CLI and MCP server do,
// so a writer that stores only in SQLite shows up here as a missing row.
func populateRandomVault(t *testing.T, vaultDir string, r *rand.Rand) {
	t.Helper()

	notes := 2 + r.IntN(4)
	for i := range notes {
		body := fmt.Sprintf("---\naliases: [alias-%d]\n---\n# Note %d\n\n%s links to [[note-%d]].\n",
			i, i, propPhrase(r, 6), r.IntN(notes))
		if err := os.WriteFile(filepath.Join(vaultDir, fmt.Sprintf("note-%d.md", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ix, err := index.Open(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := range 3 + r.IntN(6) {
		res, err := ops.Remember(ix.DB, ops.RememberRequest{Memory: memory.Memory{
			Text:        fmt.Sprintf("memory %d: %s", i, propPhrase(r, 5)),
			Kind:        propKinds[r.IntN(len(propKinds))],
			Salience:    0.7,
			Source:      "manual",
			Project:     []string{"", "propproj"}[r.IntN(2)],
			Created:     time.Now().Unix(),
			Quarantined: r.IntN(4) == 0,
		}})
		if err != nil {
			t.Fatalf("remember: %v", err)
		}
		if err := res.Outcome.Err(); err != nil {
			t.Fatalf("remember did not reach the vault: %v", err)
		}
		if res.Receipt.ID != 0 {
			ids = append(ids, res.Receipt.ID)
		}
	}
	r.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	if len(ids) > 2 && r.IntN(2) == 0 {
		if _, err := ops.Correct(ix.DB, ops.CorrectRequest{ID: ids[0],
			Memory: memory.Memory{Text: "corrected: " + propPhrase(r, 4), Created: time.Now().Unix()}}); err != nil {
			t.Fatalf("correct: %v", err)
		}
		ids = ids[1:]
	}
	if len(ids) > 2 && r.IntN(2) == 0 {
		if err := memory.Forget(ix.DB, ids[0]); err != nil {
			t.Fatalf("forget: %v", err)
		}
	}
	ix.Close()

	for i := range 1 + r.IntN(3) {
		captureStdout(t, func() {
			if err := commitmentCmd([]string{"add", fmt.Sprintf("loop %d: %s", i, propPhrase(r, 3))}); err != nil {
				t.Fatalf("loop add: %v", err)
			}
		})
	}
	for range 1 + r.IntN(3) {
		captureStdout(t, func() {
			if err := runNote([]string{"note: " + propPhrase(r, 5)}); err != nil {
				t.Fatalf("note: %v", err)
			}
		})
	}
	captureStdout(t, func() {
		if err := runCheckpoint([]string{"--next", propPhrase(r, 4), "--failed", "tried " + propPhrase(r, 4)}); err != nil {
			t.Fatalf("checkpoint: %v", err)
		}
	})
	// A note after the checkpoint, every time: an open session is the one kind
	// the session tables must restore, and a coin flip here left five seeds
	// with none, comparing two empty tables.
	captureStdout(t, func() {
		if err := runNote([]string{"after: " + propPhrase(r, 5)}); err != nil {
			t.Fatalf("note after checkpoint: %v", err)
		}
	})
}

// dumpDurable renders each compared table as sorted lines, so two indexes
// compare equal exactly when they hold the same rows in those columns.
func dumpDurable(t *testing.T, vaultDir string) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(vaultDir, ".logos", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := map[string]string{}
	for table, cols := range durableColumns {
		var exists int
		db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = ?", table).Scan(&exists)
		if exists == 0 {
			out[table] = "(no table)"
			continue
		}
		rows, err := db.Query(fmt.Sprintf("SELECT %s FROM %s %s ORDER BY %s", cols, table, durableWhere[table], cols))
		if err != nil {
			t.Fatalf("dumping %s: %v", table, err)
		}
		n := len(strings.Split(cols, ","))
		var b strings.Builder
		for rows.Next() {
			vals := make([]any, n)
			ptrs := make([]any, n)
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			cells := make([]string, n)
			for i, v := range vals {
				if bs, ok := v.([]byte); ok {
					v = string(bs)
				}
				cells[i] = fmt.Sprint(v)
			}
			fmt.Fprintln(&b, strings.Join(cells, " | "))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		out[table] = b.String()
	}
	return out
}
