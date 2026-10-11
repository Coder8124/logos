package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
)

// claudeMemSource labels every imported memory, so `logos memory forget
// --source claude-mem` undoes an import whole and recall can say where a fact
// came from.
const claudeMemSource = "claude-mem"

// claudeMemConfidence is below what an agent's own `remember` gets (0.85).
// These are another tool's machine summaries of a session, read back with no
// way to ask what they were based on: something reported, never verified.
const claudeMemConfidence = 0.6

// `logos import --from claude-mem` reads claude-mem's local store into Logos
// memories, so switching costs nobody what their old tool already learned.
//
// The same rule as bootstrap: nothing is written without a yes, because these
// are about to be recalled into an agent's context as if they were known.
func runImport(args []string) error {
	from := strings.TrimSpace(flagStr(args, "--from", ""))
	if from != claudeMemSource {
		return fmt.Errorf("usage: logos import --from claude-mem [--db PATH] [--project P] [--dry-run] [--yes]")
	}
	path := flagStr(args, "--db", "")
	if path == "" {
		path = claudeMemDBPath()
	}
	only := strings.TrimSpace(flagStr(args, "--project", ""))

	found, err := readClaudeMem(path, only)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		if only != "" {
			fmt.Printf("Nothing to import: %s has no observations for project %q.\n", path, only)
		} else {
			fmt.Printf("Nothing to import: %s has no observations.\n", path)
		}
		return nil
	}

	perProject := map[string]int{}
	for _, m := range found {
		perProject[m.Project]++
	}
	projects := make([]string, 0, len(perProject))
	for p := range perProject {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	fmt.Printf("From %s:\n\n", path)
	for _, p := range projects {
		fmt.Printf("  %-24s %d observations\n", p, perProject[p])
	}
	fmt.Println("\nA sample of what would be written:")
	for _, m := range found[:min(5, len(found))] {
		fmt.Printf("  - [%s] %s\n", m.Project, truncate(m.Text, 100))
	}
	fmt.Printf("\nEach becomes a memory in its own project, labelled source %s, confidence %.2f.\n", claudeMemSource, claudeMemConfidence)

	if hasFlag(args, "--dry-run") {
		fmt.Printf("%d memories would be written. Nothing was.\n", len(found))
		return nil
	}
	if !hasFlag(args, "--yes") && !confirmNo(fmt.Sprintf("Import these %d?", len(found))) {
		fmt.Println("Nothing written.")
		return nil
	}

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		return err
	}
	// An embedding lets a re-worded fact merge with what is already known;
	// without a runtime, or under LOGOS_EMBED=off as `logos memory add` honours
	// it, the import still runs and dedups by exact text.
	var embed *provider.Provider
	var model string
	if _, on := embedModel(); on {
		if rt, err := openRouter(); err == nil {
			if m, err := rt.Model(router.T0); err == nil {
				embed, model = rt.Local(), m
			}
		}
	}

	// Each write rewrites the kind's file, so a large store takes a while;
	// said up front so a quiet minute does not read as a hang.
	fmt.Printf("Writing %d memories…\n", len(found))
	var imported, present, refused, masked int
	for _, m := range found {
		r, err := memory.Store(ix.DB, embed, model, m)
		if errors.Is(err, memory.ErrOnlySecret) {
			refused++
			continue
		}
		if err != nil {
			// Stopping here, not skipping: whatever failed will fail the next
			// write the same way, and the counts so far are what the user needs
			// to know how much landed.
			return fmt.Errorf("imported %d, then storing %q failed: %w", imported, truncate(m.Text, 60), err)
		}
		if len(r.Redactions) > 0 {
			masked++
		}
		if r.Created() || r.Queued() {
			imported++
		} else {
			present++
		}
	}

	fmt.Printf("\nImported %d, skipped %d (already present), refused %d (nothing but a secret).\n", imported, present, refused)
	if masked > 0 {
		fmt.Printf("%d had a secret-shaped token masked before it was written.\n", masked)
	}
	if embed == nil {
		fmt.Println("No embedding model, so these were matched by exact text; a later import with one will merge near-duplicates.")
	}
	fmt.Printf("To undo all of it: logos memory forget --source %s\n", claudeMemSource)
	return nil
}

// claudeMemDBPath is where claude-mem keeps its store, resolved the way
// claude-mem resolves it (src/shared/paths.ts): CLAUDE_MEM_DATA_DIR, then the
// same key in ~/.claude-mem/settings.json (flat, or under "env"), then
// ~/.claude-mem. Missing a relocated store would report an install as empty.
func claudeMemDBPath() string {
	home, _ := os.UserHomeDir()
	dir := os.Getenv("CLAUDE_MEM_DATA_DIR")
	if dir == "" {
		dir = filepath.Join(home, ".claude-mem")
		var doc map[string]any
		if raw, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil && json.Unmarshal(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), &doc) == nil {
			if env, ok := doc["env"].(map[string]any); ok {
				doc = env
			}
			if d, ok := doc["CLAUDE_MEM_DATA_DIR"].(string); ok && d != "" {
				dir = d
			}
		}
	}
	if dir == "~" {
		dir = home
	} else if strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(home, dir[2:])
	}
	return filepath.Join(dir, "claude-mem.db")
}

// readClaudeMem turns claude-mem observations into memories, unstored.
//
// The database is opened read-only: it belongs to another tool that may be
// running, and an importer that can write to its source can damage it. Columns
// are asked for rather than assumed, because claude-mem adds them by migration
// and an install that has not run the newest one is still an install someone
// wants to leave.
func readClaudeMem(path, only string) ([]*memory.Memory, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no claude-mem store at %s (set CLAUDE_MEM_DATA_DIR or pass --db): %w", path, err)
	}
	// Escaped because in a URI `?` starts the query, `#` the fragment and `%`
	// an escape: a store in a directory named with one passed the Stat above
	// and then failed to open. Only those three, so a Windows path is
	// otherwise the string it was.
	uriPath := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(path)
	db, err := sql.Open("sqlite", "file:"+uriPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	cols := map[string]bool{}
	rows, err := db.Query("SELECT name FROM pragma_table_info('observations')")
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return nil, err
		}
		cols[c] = true
	}
	rows.Close()
	if !cols["project"] {
		return nil, fmt.Errorf("%s has no observations table this importer can read — is it a claude-mem store?", path)
	}
	col := func(name string) string {
		if cols[name] {
			return "COALESCE(" + name + ", '')"
		}
		return "''"
	}
	order := "rowid"
	if cols["created_at_epoch"] {
		order = "created_at_epoch, rowid"
	}
	q := fmt.Sprintf("SELECT project, %s, %s, %s, %s, %s FROM observations ORDER BY %s",
		col("merged_into_project"), col("title"), col("subtitle"), col("text"), col("narrative"), order)
	rows, err = db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("reading observations from %s: %w", path, err)
	}
	defer rows.Close()

	var out []*memory.Memory
	for rows.Next() {
		var project, merged, title, subtitle, text, narrative string
		if err := rows.Scan(&project, &merged, &title, &subtitle, &text, &narrative); err != nil {
			return nil, err
		}
		// claude-mem's own answer to "which project is this now" when two
		// were merged; the original name would scatter one project into two.
		if merged != "" {
			project = merged
		}
		if only != "" && project != only {
			continue
		}
		body := observationText(title, subtitle, text, narrative)
		if body == "" {
			continue
		}
		out = append(out, &memory.Memory{
			Text:       body,
			Kind:       memory.Fact,
			Confidence: claudeMemConfidence,
			Project:    project,
			Source:     claudeMemSource,
		})
	}
	return out, rows.Err()
}

// observationText is the one line a memory should be. The title is
// claude-mem's own one-line summary, so it leads; the narrative is a
// paragraph and is used only when there is nothing shorter.
func observationText(title, subtitle, text, narrative string) string {
	clean := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	title, subtitle = clean(title), clean(subtitle)
	switch {
	case title != "" && subtitle != "":
		return title + " — " + subtitle
	case title != "":
		return title
	case clean(text) != "":
		return clean(text)
	default:
		return clean(narrative)
	}
}
