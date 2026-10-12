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
const (
	claudeMDSource    = "claude-md"
	cursorRulesSource = "cursor-rules"
	// Below an agent's own `remember` (0.85), as claude-mem's are: a rule
	// file says what someone wanted once, and nothing says it still holds.
	ruleConfidence = 0.6
)

type ruleFile struct {
	shown   string // the path as the dry run names it
	project string // "" for a file that applies to every repository
	items   []string
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
		for _, item := range f.items {
			found = append(found, &memory.Memory{
				Text: item, Kind: memory.Preference, Project: f.project,
				Source: from, Confidence: ruleConfidence, Quarantined: true,
			})
		}
	}
	if len(found) == 0 {
		fmt.Printf("Nothing to import: no %s rules in %s or ~/.claude.\n", from, scope.DenyRoot(dir))
		return nil
	}

	fmt.Println("Found:")
	for _, f := range files {
		where := "project " + f.project
		if f.project == "" {
			where = "every project"
		}
		fmt.Printf("  %-36s %3d %s, for %s\n", f.shown, len(f.items), plural(len(f.items), "rule"), where)
	}
	fmt.Println("\nA sample of what would be queued:")
	for _, m := range found[:min(5, len(found))] {
		fmt.Printf("  - %s\n", truncate(m.Text, 100))
	}
	fmt.Printf("\nEach becomes a proposal in the review queue, labelled source %s, confidence %.2f. Most of a rules file is convention, so none of it is recalled until you accept it.\n", from, ruleConfidence)

	if hasFlag(args, "--dry-run") {
		fmt.Printf("%d proposals would be queued. Nothing was.\n", len(found))
		return nil
	}
	if !hasFlag(args, "--yes") && !confirmNo(fmt.Sprintf("Queue these %d?", len(found))) {
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
	offered, dismissed, err := notDismissed(ix.DB, found)
	if err != nil {
		return err
	}
	c, err := storeImported(ix.DB, offered)
	if err != nil {
		return err
	}
	fmt.Printf("\nImported %d, skipped %d (already present), refused %d (nothing but a secret).\n", c.imported, c.present+dismissed, c.refused)
	if dismissed > 0 {
		fmt.Printf("Of those skipped, %d you rejected or forgot before; a rules file read again does not overrule that.\n", dismissed)
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
// What the timeline has met and no row still holds is what was dismissed; the
// timeline is restored from log.md, so this survives deleting the index. The
// text is masked first because the timeline holds it as Store wrote it.
func notDismissed(db *sql.DB, found []*memory.Memory) ([]*memory.Memory, int, error) {
	seen, err := memory.SeenTexts(db)
	if err != nil {
		return nil, 0, err
	}
	rows, err := db.Query("SELECT text FROM memories")
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	held := map[string]bool{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, 0, err
		}
		held[t] = true
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var out []*memory.Memory
	dismissed := 0
	for _, m := range found {
		t, _ := secret.Mask("memory", m.Text)
		if seen[t] && !held[t] {
			dismissed++
			continue
		}
		out = append(out, m)
	}
	return out, dismissed, nil
}

// readRuleFiles reads every rules file the source names, in the repository at
// root and, for claude-md, in ~/.claude. A file that is missing is skipped; one
// that is there and cannot be read is an error, because an import that quietly
// left out a file would report a count the user takes as everything.
func readRuleFiles(from, root, project string) ([]ruleFile, error) {
	home, _ := os.UserHomeDir()
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
	seen := map[string]bool{}
	for _, c := range cands {
		// Run from home, the repository's .claude/CLAUDE.md is the global
		// one; read twice, it would be offered twice.
		if seen[c.path] {
			continue
		}
		seen[c.path] = true
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
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		switch {
		case strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"):
			flush()
			fence = t[:3]
		case t == "", t == "---", ruleHeading.MatchString(t), strings.HasPrefix(t, "|"), strings.HasPrefix(t, "<!--"):
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
