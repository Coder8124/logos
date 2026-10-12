package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/claudemem"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
	"github.com/Coder8124/logos/internal/secret"
	"github.com/Coder8124/logos/internal/untrusted"
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
	switch from {
	case claudeMDSource, cursorRulesSource:
		return runImportRules(from, args)
	case "git":
		// Named here so the one verb lists every way in; git history is what
		// bootstrap already reads.
		return runBootstrap(withoutFlag(args, "--from"))
	}
	if from != claudeMemSource {
		return fmt.Errorf("usage: logos import --from claude-mem [--db PATH] [--project P] [--dry-run] [--yes]\n       logos import --from claude-mem --auto   (what the import switch runs)\n       logos import --from claude-md|cursor-rules [--dir DIR] [--project P] [--dry-run] [--yes]\n       logos import --from git ...             (the same as logos bootstrap)")
	}
	if hasFlag(args, "--auto") {
		return runImportAuto()
	}
	path := flagStr(args, "--db", "")
	if path == "" {
		path = claudemem.DBPath()
	}
	only := strings.TrimSpace(flagStr(args, "--project", ""))

	found, _, err := readClaudeMem(path, only, 0)
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
	// Each write rewrites the kind's file, so a large store takes a while;
	// said up front so a quiet minute does not read as a hang.
	fmt.Printf("Writing %d memories…\n", len(found))
	c, err := storeImported(ix.DB, found)
	if err != nil {
		return err
	}

	fmt.Printf("\nImported %d, skipped %d (already present), refused %d (nothing but a secret).\n", c.imported, c.present, c.refused)
	if c.masked > 0 {
		fmt.Printf("%d had a secret-shaped token masked before it was written.\n", c.masked)
	}
	if !c.embedded {
		fmt.Println("No embedding model, so these were matched by exact text; a later import with one will merge near-duplicates.")
	}
	fmt.Printf("To undo all of it: logos memory forget --source %s\n", claudeMemSource)
	return nil
}

type importCounts struct {
	imported, present, refused, masked int
	embedded                           bool
	// through is the last claude-mem row an automatic import has read.
	through int
}

// storeImported writes memories read from another tool, with the counts both
// the hand-run import and the import switch report.
func storeImported(db *sql.DB, found []*memory.Memory) (importCounts, error) {
	var c importCounts
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
	c.embedded = embed != nil
	for _, m := range found {
		r, err := memory.Store(db, embed, model, m)
		if errors.Is(err, memory.ErrOnlySecret) {
			c.refused++
			continue
		}
		if err != nil {
			// Stopping here, not skipping: whatever failed will fail the next
			// write the same way, and the counts so far are what the user needs
			// to know how much landed.
			return c, fmt.Errorf("imported %d, then storing %q failed: %w", c.imported, truncate(m.Text, 60), err)
		}
		if len(r.Redactions) > 0 {
			c.masked++
		}
		if r.Created() || r.Queued() {
			c.imported++
		} else {
			c.present++
		}
	}
	return c, nil
}

// runImportAuto is the import the import switch launches at session start, in
// a process of its own with nobody to answer a yes: turning the switch on was
// the yes. What it did goes to auto-log.md, which the next resume reads out.
//
// Only what Logos has never recorded is offered to Store. Store's own dedup
// would skip what is still in memory, but not what the user forgot: a forgotten
// import coming back every time claude-mem writes would overrule them.
func runImportAuto() error {
	n, err := importAuto()
	if err != nil {
		logAuto(auto.Entry{Switch: auto.Import, Failed: untrusted.Inline(err.Error())})
		return err
	}
	logAuto(auto.Entry{Switch: auto.Import, N: n.imported, Counts: []auto.Count{
		{Name: "present", N: n.present}, {Name: "refused", N: n.refused}, {Name: "through", N: n.through}}})
	return nil
}

// importedThrough is the last claude-mem row the last automatic import that
// finished read, or 0. claude-mem changes nearly every session, and reading
// its whole store each time re-offered every observation refused as nothing
// but a key, so every resume repeated the same refusal. Kept in auto-log.md,
// in the vault, so deleting the index does not start the store over.
func importedThrough(vault string) (int, error) {
	entries, err := auto.Entries(vault, "", time.Time{})
	if err != nil {
		return 0, err
	}
	through := 0
	for _, e := range entries {
		if e.Switch != auto.Import || e.Started || e.Failed != "" {
			continue
		}
		for _, c := range e.Counts {
			if c.Name == "through" {
				through = c.N
			}
		}
	}
	return through, nil
}

func importAuto() (importCounts, error) {
	var c importCounts
	ix, err := openIndexQuiet()
	if err != nil {
		return c, err
	}
	defer ix.Close()
	if !auto.On(ix.Vault, auto.Import) {
		return c, nil
	}
	// What Logos has already seen, and so what a forgotten import is, is read
	// from the index. An index that was deleted would let every memory the user
	// had forgotten back in, so it is rebuilt from the vault first — this is a
	// background process, with no command waiting on it.
	synced, err := ix.Synced()
	if err != nil {
		return c, err
	}
	if !synced {
		if err := indexPass(ix, io.Discard, nil, ""); err != nil {
			return c, fmt.Errorf("the index had to be rebuilt to tell what was already imported, and the rebuild failed, so nothing was imported: %w", err)
		}
		logAutoIn(ix.Vault, auto.Entry{Switch: auto.Index, N: 1})
	}
	if err := memory.Init(ix.DB); err != nil {
		return c, err
	}
	after, err := importedThrough(ix.Vault)
	if err != nil {
		return c, err
	}
	found, through, err := readClaudeMem(claudemem.DBPath(), "", after)
	if err != nil {
		return c, err
	}
	c.through = max(after, through)
	seen, err := memory.SeenTexts(ix.DB)
	if err != nil {
		return c, err
	}
	var fresh []*memory.Memory
	for _, m := range found {
		// Compared as Store will write it: the timeline holds the masked
		// text, and a forgotten import with a key in it is still forgotten.
		if masked, _ := secret.Mask("memory", m.Text); seen[masked] {
			c.present++
			continue
		}
		fresh = append(fresh, m)
	}
	got, err := storeImported(ix.DB, fresh)
	got.present, got.through = got.present+c.present, c.through
	return got, err
}

// readClaudeMem turns claude-mem observations into memories, unstored.
//
// The database is opened read-only: it belongs to another tool that may be
// running, and an importer that can write to its source can damage it. Columns
// are asked for rather than assumed, because claude-mem adds them by migration
// and an install that has not run the newest one is still an install someone
// wants to leave.
//
// Only rows after the rowid after are read, and the last rowid read is
// returned with them.
func readClaudeMem(path, only string, after int) ([]*memory.Memory, int, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, 0, fmt.Errorf("no claude-mem store at %s (set CLAUDE_MEM_DATA_DIR or pass --db): %w", path, err)
	}
	// Escaped because in a URI `?` starts the query, `#` the fragment and `%`
	// an escape: a store in a directory named with one passed the Stat above
	// and then failed to open. Only those three, so a Windows path is
	// otherwise the string it was.
	uriPath := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(path)
	db, err := sql.Open("sqlite", "file:"+uriPath+"?mode=ro")
	if err != nil {
		return nil, 0, err
	}
	defer db.Close()

	cols := map[string]bool{}
	rows, err := db.Query("SELECT name FROM pragma_table_info('observations')")
	if err != nil {
		return nil, 0, fmt.Errorf("reading %s: %w", path, err)
	}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return nil, 0, err
		}
		cols[c] = true
	}
	rows.Close()
	if !cols["project"] {
		return nil, 0, fmt.Errorf("%s has no observations table this importer can read — is it a claude-mem store?", path)
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
	q := fmt.Sprintf("SELECT rowid, project, %s, %s, %s, %s, %s FROM observations WHERE rowid > ? ORDER BY %s",
		col("merged_into_project"), col("title"), col("subtitle"), col("text"), col("narrative"), order)
	// A store whose rows end before the mark is a new store — claude-mem
	// reinstalled, its ids starting over — and all of it is unread.
	var last int
	if err := db.QueryRow("SELECT COALESCE(MAX(rowid), 0) FROM observations").Scan(&last); err != nil {
		return nil, 0, fmt.Errorf("reading observations from %s: %w", path, err)
	}
	if last < after {
		after = 0
	}
	rows, err = db.Query(q, after)
	if err != nil {
		return nil, 0, fmt.Errorf("reading observations from %s: %w", path, err)
	}
	defer rows.Close()

	var out []*memory.Memory
	through := 0
	for rows.Next() {
		var id int
		var project, merged, title, subtitle, text, narrative string
		if err := rows.Scan(&id, &project, &merged, &title, &subtitle, &text, &narrative); err != nil {
			return nil, 0, err
		}
		through = max(through, id)
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
	return out, through, rows.Err()
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

// withoutFlag drops a valued flag and its value, for handing the rest of a
// command line to another verb that does not know the flag.
func withoutFlag(args []string, name string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == name {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}
