package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/scope"
	"github.com/Coder8124/logos/internal/secret"
)

// The two rules-file importers. Their records go to the review queue, not to
// memory: most of a CLAUDE.md or a Cursor rule is convention — how commits are
// worded, which linter runs — and convention belongs in CONTRIBUTING.md, where
// every contributor reads it, not in one person's recall. A person sorts the
// operational few from the rest; until they do, nothing here is recalled.
//
// Claude Code's auto memory goes the same way for a different reason: its
// notes are what an agent decided to write down, and no agent's write grants
// trust. The point of importing them is that they stop being Claude Code's
// alone — Codex, Cursor and a teammate's agent never read ~/.claude.
const (
	claudeMDSource         = "claude-md"
	cursorRulesSource      = "cursor-rules"
	claudeAutoMemorySource = "claude-auto-memory"
	// Below an agent's own `remember` (0.85), as claude-mem's are: a rule
	// file says what someone wanted once, and nothing says it still holds.
	ruleConfidence = 0.6
)

type ruleFile struct {
	shown   string // the path as the dry run names it
	project string // "" for a file that applies to every repository
	items   []string
	kind    memory.Kind // "" for a rules file, whose items are preferences
}

func runImportRules(from string, args []string) error {
	dir := flagStr(args, "--dir", "")
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = cwd
	}
	project := strings.TrimSpace(flagStr(args, "--project", ""))
	if project == "" {
		project = projectFor(dir)
	}
	files, err := readRuleFiles(from, scope.DenyRoot(dir), project)
	if err != nil {
		return err
	}
	var found []*memory.Memory
	for _, f := range files {
		kind := f.kind
		if kind == "" {
			kind = memory.Preference
		}
		for _, item := range f.items {
			found = append(found, &memory.Memory{
				Text: item, Kind: kind, Project: f.project,
				Source: from, Confidence: ruleConfidence, Quarantined: true,
			})
		}
	}
	if len(found) == 0 {
		where := scope.DenyRoot(dir)
		switch from {
		case claudeMDSource:
			where += " or ~/.claude"
		case claudeAutoMemorySource:
			// The folder is named, because a moved one (autoMemoryDirectory)
			// is the likeliest reason it is empty, and "none" would read as
			// Claude Code never having saved anything.
			where = autoMemoryDir(dir)
		}
		fmt.Printf("Nothing to import: no %s %s in %s.\n", from, noun(from, 2), where)
		return nil
	}

	// Opened before the preview, not after the yes: the dry run and the prompt
	// count what the import will offer, and a rule the user already rejected
	// is not offered. Quiet, because a missing index is rebuilt below rather
	// than warned about.
	ix, err := openIndexQuiet()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		return err
	}
	// What was rejected is read from the index, and a deleted one reads as
	// nothing rejected: every rule the user threw out was queued again. So it
	// is rebuilt from the vault first, as the import switch does.
	synced, err := ix.Synced()
	if err != nil {
		return err
	}
	if !synced {
		// To stderr, as rebuildIndex does: the pass reports what it restored
		// and wrote back, and a repair nobody hears about reads as none.
		if err := indexPass(ix, os.Stderr, nil, ""); err != nil {
			return fmt.Errorf("the index had to be rebuilt to tell which rules you rejected, and the rebuild failed, so nothing was imported: %w", err)
		}
		fmt.Println("The index was missing, so it was rebuilt from the vault first: what you rejected before is read from it.")
	}
	fresh, dismissed, present, err := notDismissed(ix.DB, found)
	if err != nil {
		return err
	}

	fmt.Println("Found:")
	for _, f := range files {
		where := "project " + f.project
		if f.project == "" {
			where = "every project"
		}
		fmt.Printf("  %-36s %3d %s, for %s\n", f.shown, len(f.items), noun(from, len(f.items)), where)
	}
	if len(fresh) > 0 {
		fmt.Println("\nA sample of what would be queued:")
		for _, m := range fresh[:min(5, len(fresh))] {
			fmt.Printf("  - %s\n", truncate(m.Text, 100))
		}
	}
	why := "Most of a rules file is convention, so none of it is recalled until you accept it."
	if from == claudeAutoMemorySource {
		why = "An agent wrote these, and an agent's word is not a yes, so none is recalled until you accept it."
	}
	fmt.Printf("\nEach becomes a proposal in the review queue, labelled source %s, confidence %.2f. %s\n", from, ruleConfidence, why)
	if dismissed > 0 {
		fmt.Printf("%d you rejected or forgot before; reading the files again does not overrule that, so they are left out.\n", dismissed)
	}

	if hasFlag(args, "--dry-run") {
		fmt.Printf("%d %s would be queued, %d already in the queue or in memory. Nothing was written.\n", len(fresh), plural(len(fresh), "proposal"), present)
		return nil
	}
	if len(fresh) > 0 && !hasFlag(args, "--yes") && !confirmNo(fmt.Sprintf("Queue %d %s?", len(fresh), plural(len(fresh), "proposal"))) {
		fmt.Println("Nothing written.")
		return nil
	}

	// Only what is new goes to Store: a rule it already holds would be stored
	// again as a reinforcement, a line in the vault's log for every rule on
	// every re-import, and reading a file twice is not new evidence.
	var c importCounts
	if len(fresh) > 0 {
		if c, err = storeImported(ix.DB, fresh); err != nil {
			return err
		}
	}
	fmt.Printf("\nImported %d, skipped %d (already present), refused %d (nothing but a secret).\n", c.imported, present+c.present+dismissed, c.refused)
	if dismissed > 0 {
		fmt.Printf("Of those skipped, %d you rejected or forgot before.\n", dismissed)
	}
	if c.masked > 0 {
		fmt.Printf("%d had a secret-shaped token masked before it was written.\n", c.masked)
	}
	fmt.Println("They wait in `logos review`; accept the ones that are operational and leave the conventions to CONTRIBUTING.md.")
	fmt.Printf("To undo all of it: logos memory forget --source %s\n", from)
	return nil
}

// notDismissed drops the rules the user already threw out. A rules file is read
// whole on every import, and Reject and Forget delete the row Store would have
// deduplicated against, so a rejected rule was queued again by the next import.
// A text the timeline records as rejected or forgotten, that no row holds
// again since, was dismissed; the timeline is restored from log.md, so this
// survives deleting the index. The text is masked first because the timeline
// holds it as Store wrote it.
//
// fresh is what is left once the rules a row already holds, by the fingerprint
// Store deduplicates on, are counted in present instead. With an embedding
// model, Store also folds a reworded near-duplicate into a held row, so fresh
// is what the import offers at most, and its summary counts what it wrote.
func notDismissed(db *sql.DB, found []*memory.Memory) (fresh []*memory.Memory, dismissed, present int, err error) {
	seen, err := memory.DismissedTexts(db)
	if err != nil {
		return nil, 0, 0, err
	}
	rows, err := db.Query("SELECT fingerprint FROM memories")
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	held := map[string]bool{}
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			return nil, 0, 0, err
		}
		held[fp] = true
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}
	for _, m := range found {
		t, _ := secret.Mask("memory", m.Text)
		fp := memory.Fingerprint(t)
		if seen[t] && !held[fp] {
			dismissed++
			continue
		}
		if held[fp] {
			present++
			continue
		}
		// The same rule in two files is stored once; the second is skipped.
		held[fp] = true
		fresh = append(fresh, m)
	}
	return fresh, dismissed, present, nil
}

// noun is what one item of the source is called, in the count n.
func noun(from string, n int) string {
	if from == claudeAutoMemorySource {
		return plural(n, "memory")
	}
	return plural(n, "rule")
}

// readRuleFiles reads every rules file the source names, in the repository at
// root and, for claude-md, in ~/.claude. A file that is missing is skipped; one
// that is there and cannot be read is an error, because an import that quietly
// left out a file would report a count the user takes as everything.
func readRuleFiles(from, root, project string) ([]ruleFile, error) {
	home, _ := os.UserHomeDir()
	if from == claudeAutoMemorySource {
		return readAutoMemory(autoMemoryDir(root), home, project)
	}
	type cand struct {
		path, project string
	}
	var cands []cand
	switch from {
	case claudeMDSource:
		for _, name := range []string{"CLAUDE.md", "AGENTS.md", filepath.Join(".claude", "CLAUDE.md")} {
			cands = append(cands, cand{filepath.Join(root, name), project})
		}
		if home != "" {
			for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
				cands = append(cands, cand{filepath.Join(home, ".claude", name), ""})
			}
		}
	case cursorRulesSource:
		cands = append(cands, cand{filepath.Join(root, ".cursorrules"), project})
		err := filepath.WalkDir(filepath.Join(root, ".cursor", "rules"), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".mdc") {
				cands = append(cands, cand{p, project})
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}

	var out []ruleFile
	// By the file a name resolves to: AGENTS.md is often a link to CLAUDE.md,
	// and read under both names every rule was offered twice. The value is
	// the file's place in out, or -1 for one with no rules.
	seen := map[string]int{}
	for _, c := range cands {
		key := c.path
		if real, err := filepath.EvalSymlinks(c.path); err == nil {
			key = real
		}
		if i, ok := seen[key]; ok {
			// Run from home, the repository's .claude/CLAUDE.md is the
			// global one. It applies to every project, whichever name
			// reached it first.
			if i >= 0 && c.project == "" {
				out[i].project = ""
			}
			continue
		}
		seen[key] = -1
		raw, err := os.ReadFile(c.path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		items := ruleItems(string(raw))
		if len(items) == 0 {
			continue
		}
		shown := c.path
		if rel, err := filepath.Rel(root, c.path); err == nil && !strings.HasPrefix(rel, "..") {
			shown = rel
		} else if home != "" && strings.HasPrefix(c.path, home+string(filepath.Separator)) {
			shown = "~" + c.path[len(home):]
		}
		seen[key] = len(out)
		out = append(out, ruleFile{shown: shown, project: c.project, items: items})
	}
	return out, nil
}

var (
	ruleHeading  = regexp.MustCompile(`^#{1,6}(\s|$)`)
	ruleListItem = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s+(.*)$`)
)

// ruleItems splits a rules file into the units a person reviews one at a
// time: each list item, with the lines that continue it, and each paragraph.
// Headings, tables and code blocks are structure, not rules. A lead-in ending
// in a colon is dropped: alone in the queue it says nothing, and the items it
// introduces arrive on their own.
func ruleItems(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	// An .mdc file's frontmatter is Cursor's configuration — globs, whether
	// the rule always applies — not something anyone said.
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if end := strings.Index(rest, "\n---"); end >= 0 {
			text = rest[end+len("\n---"):]
		}
	}
	var items, cur []string
	flush := func() {
		s := strings.Join(strings.Fields(strings.Join(cur, " ")), " ")
		cur = nil
		if s != "" && !strings.HasSuffix(s, ":") {
			items = append(items, s)
		}
	}
	fence := ""
	comment := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		// A note hidden from the rendered file is hidden for a reason, all of
		// it, wherever on the line it opens and closes. A line that was only
		// a comment ends the paragraph, as it did before it was hidden.
		line, comment = uncommented(line, comment)
		t = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"):
			flush()
			fence = t[:3]
		case t == "", t == "---", ruleHeading.MatchString(t), strings.HasPrefix(t, "|"):
			flush()
		default:
			if m := ruleListItem.FindStringSubmatch(t); m != nil {
				flush()
				t = m[1]
			}
			cur = append(cur, t)
		}
	}
	flush()
	return items
}

// uncommented returns line with its HTML comments cut out, given whether one
// was already open when it began, and whether one is still open at its end.
func uncommented(line string, open bool) (string, bool) {
	var b strings.Builder
	for {
		if open {
			end := strings.Index(line, "-->")
			if end < 0 {
				return b.String(), true
			}
			line, open = line[end+len("-->"):], false
		}
		start := strings.Index(line, "<!--")
		if start < 0 {
			b.WriteString(line)
			return b.String(), false
		}
		b.WriteString(line[:start])
		line, open = line[start+len("<!--"):], true
	}
}

// autoMemoryDir is where Claude Code keeps auto memory for the repository at
// root: <config>/projects/<root with every non-alphanumeric rune as "-">/memory.
// The naming is read off Claude Code's own folders, not a documented contract
// (/Users/a/brain is -Users-a-brain), so a miss names the folder it looked in.
func autoMemoryDir(root string) string {
	config := os.Getenv("CLAUDE_CONFIG_DIR")
	if config == "" {
		home, _ := os.UserHomeDir()
		config = filepath.Join(home, ".claude")
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	name := strings.Map(func(r rune) rune {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, root)
	return filepath.Join(config, "projects", name, "memory")
}

// autoMemoryKinds maps auto memory's four types onto Logos's kinds. feedback is
// how the user wants the agent to work, which is what a preference is; a type
// this does not know is kept as a fact rather than dropped.
var autoMemoryKinds = map[string]memory.Kind{
	"user":      memory.Preference,
	"feedback":  memory.Preference,
	"project":   memory.Context,
	"reference": memory.Fact,
}

var autoMemoryType = regexp.MustCompile(`(?m)^\s*type:\s*["']?([A-Za-z]+)`)

// readAutoMemory reads one memory per note in dir. MEMORY.md is skipped: it is
// the index Claude Code loads, one pointer line per note, and imported it would
// offer every memory a second time as a fragment.
func readAutoMemory(dir, home, project string) ([]ruleFile, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []ruleFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "MEMORY.md" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		text := strings.ReplaceAll(string(raw), "\r\n", "\n")
		kind := memory.Fact
		var desc string
		if rest, ok := strings.CutPrefix(text, "---\n"); ok {
			if end := strings.Index(rest, "\n---"); end >= 0 {
				front := rest[:end]
				text = rest[end+len("\n---"):]
				if m := autoMemoryType.FindStringSubmatch(front); m != nil {
					if k, ok := autoMemoryKinds[strings.ToLower(m[1])]; ok {
						kind = k
					}
				}
				for _, line := range strings.Split(front, "\n") {
					if v, ok := strings.CutPrefix(strings.TrimSpace(line), "description:"); ok {
						desc = strings.Trim(strings.TrimSpace(v), `"'`)
					}
				}
			}
		}
		// The body is the memory, the Why and How lines with it: they are what
		// makes a rule something the next agent can judge rather than obey.
		// A note with no body still said something in its description.
		body := strings.Join(strings.Fields(text), " ")
		if body == "" {
			body = desc
		}
		if body == "" {
			continue
		}
		shown := p
		if home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
			shown = "~" + p[len(home):]
		}
		out = append(out, ruleFile{shown: shown, project: project, items: []string{body}, kind: kind})
	}
	return out, nil
}
