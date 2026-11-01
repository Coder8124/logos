package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/scope"
	"github.com/Coder8124/logos/internal/secret"
	"github.com/Coder8124/logos/internal/transcript"
	"gopkg.in/yaml.v3"
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
	skip    string      // why a file read had nothing to offer, said in the report
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
	for _, f := range files {
		if f.skip != "" {
			fmt.Printf("Left out %s: %s.\n", f.shown, f.skip)
		}
	}
	if len(found) == 0 {
		where := scope.DenyRoot(dir)
		switch from {
		case claudeMDSource:
			// The folder that was read, which CLAUDE_CONFIG_DIR may have moved.
			config := claudeConfigDir()
			if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(config, home+string(filepath.Separator)) {
				config = "~" + config[len(home):]
			}
			where += " or " + config
		case claudeAutoMemorySource:
			// The folder is named, because a moved one (autoMemoryDirectory)
			// is the likeliest reason it is empty, and "none" would read as
			// Claude Code never having saved anything.
			where = autoMemoryDir(scope.DenyRoot(dir))
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
		if f.skip != "" {
			continue
		}
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
	if from == claudeAutoMemorySource {
		fmt.Println("They wait in `logos review`; what you accept is recalled by every agent that reads this vault, not only Claude Code.")
	} else {
		fmt.Println("They wait in `logos review`; accept the ones that are operational and leave the conventions to CONTRIBUTING.md.")
	}
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
// root and, for claude-md, in Claude Code's config directory. A file that is
// missing is skipped; one that is there and cannot be read is an error,
// because an import that quietly left out a file would report a count the user
// takes as everything.
func readRuleFiles(from, root, project string) ([]ruleFile, error) {
	home, _ := os.UserHomeDir()
	if from == claudeAutoMemorySource {
		return readAutoMemory(autoMemoryDir(root), home, project)
	}
	type cand struct {
		path, project string
	}
	var cands []cand
	add := func(project string, paths ...string) {
		for _, p := range paths {
			cands = append(cands, cand{p, project})
		}
	}
	switch from {
	case claudeMDSource:
		// Every place Claude Code loads instructions from, so the import
		// offers what the agent was actually told: CLAUDE.local.md and
		// .claude/rules are as much the project's rules as CLAUDE.md is.
		for _, name := range []string{"CLAUDE.md", "AGENTS.md", "CLAUDE.local.md", filepath.Join(".claude", "CLAUDE.md"), filepath.Join(".claude", "AGENTS.md")} {
			add(project, filepath.Join(root, name))
		}
		rules, err := filesUnder(filepath.Join(root, ".claude", "rules"), ".md")
		if err != nil {
			return nil, err
		}
		add(project, rules...)
		if config := claudeConfigDir(); config != "" {
			add("", filepath.Join(config, "CLAUDE.md"), filepath.Join(config, "AGENTS.md"))
			rules, err := filesUnder(filepath.Join(config, "rules"), ".md")
			if err != nil {
				return nil, err
			}
			add("", rules...)
		}
	case cursorRulesSource:
		add(project, filepath.Join(root, ".cursorrules"))
		rules, err := filesUnder(filepath.Join(root, ".cursor", "rules"), ".mdc")
		if err != nil {
			return nil, err
		}
		add(project, rules...)
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

// claudeConfigDir is where Claude Code keeps its user-level files:
// CLAUDE_CONFIG_DIR when it is set, as Claude Code itself reads it, otherwise
// ~/.claude. Empty when neither can be named.
func claudeConfigDir() string {
	if config := os.Getenv("CLAUDE_CONFIG_DIR"); config != "" {
		return config
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// filesUnder is every file below dir whose name ends in ext, in name order, with
// linked folders followed: Claude Code reads a rules folder through links,
// and a team that shares rules does it by linking one in. Each real folder is
// walked once, so a link back up the tree ends rather than looping. A dir
// that is not there is no files.
func filesUnder(dir, ext string) ([]string, error) {
	var out []string
	walked := map[string]bool{}
	var walk func(dir string) error
	walk = func(dir string) error {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return err
		}
		if walked[real] {
			return nil
		}
		walked[real] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			info, err := os.Stat(p)
			if err != nil {
				// A dangling link names nothing to read.
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return err
			}
			if info.IsDir() {
				if err := walk(p); err != nil {
					return err
				}
			} else if strings.HasSuffix(e.Name(), ext) {
				out = append(out, p)
			}
		}
		return nil
	}
	if err := walk(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
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
// root. An autoMemoryDirectory setting names the folder outright. Otherwise it
// is <projects>/<name>/memory, where the name is CLAUDE_CODE_PROJECT_DIR_NAME
// when Claude Code would honour it, or else the path with every
// non-alphanumeric rune as "-". The path is the main checkout's, symlinks
// resolved: Claude Code shares one auto memory across a repository's worktrees
// and records the physical path, so a worktree or a symlinked ~/code looked
// for a folder that never existed. The naming is read off Claude Code's own
// folders, not a documented contract, so a miss names the folder it looked in.
func autoMemoryDir(root string) string {
	config := claudeConfigDir()
	if dir := autoMemoryDirectorySetting(root, config); dir != "" {
		return dir
	}
	projects := os.Getenv(transcript.LogosClaudeProjectsEnv)
	if projects == "" {
		projects = filepath.Join(config, "projects")
	}
	// Claude Code takes the name only beside CLAUDE_CONFIG_DIR and only when
	// it is a plain folder name; otherwise it ignores it, and so must this.
	if name := os.Getenv("CLAUDE_CODE_PROJECT_DIR_NAME"); os.Getenv("CLAUDE_CONFIG_DIR") != "" && projectDirName.MatchString(name) {
		return filepath.Join(projects, name, "memory")
	}
	if _, checkout := gitstate.Identity(root); checkout != "" {
		root = checkout
	} else if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
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
	return filepath.Join(projects, name, "memory")
}

var projectDirName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// autoMemoryDirectorySetting is the autoMemoryDirectory Claude Code would use
// for the repository at root, in its precedence: local settings, then the
// project's, then the user's. Only an absolute path or one under ~/ counts,
// as in Claude Code; any other value is passed over, not guessed at. Empty
// when no settings file moves auto memory.
func autoMemoryDirectorySetting(root, config string) string {
	home, _ := os.UserHomeDir()
	for _, path := range []string{
		filepath.Join(root, ".claude", "settings.local.json"),
		filepath.Join(root, ".claude", "settings.json"),
		filepath.Join(config, "settings.json"),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var settings struct {
			AutoMemoryDirectory string `json:"autoMemoryDirectory"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(string(raw), "\ufeff")), &settings) != nil {
			continue
		}
		dir := settings.AutoMemoryDirectory
		if rest, ok := strings.CutPrefix(dir, "~/"); ok && home != "" {
			return filepath.Join(home, rest)
		}
		if filepath.IsAbs(dir) {
			return filepath.Clean(dir)
		}
	}
	return ""
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

type autoMemoryFront struct {
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
	Metadata    struct {
		Type string `yaml:"type"`
	} `yaml:"metadata"`
}

// readAutoMemory reads one memory per note in dir. MEMORY.md is skipped: it is
// the index Claude Code loads, one pointer line per note, and imported it would
// offer every memory a second time as a fragment. Anything else left out is
// listed with its reason, so the count is never taken for everything there was.
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
		if e.Name() == "MEMORY.md" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		shown := p
		if home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
			shown = "~" + p[len(home):]
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			out = append(out, ruleFile{shown: shown, skip: "not a note"})
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		front, body, err := splitAutoMemory(string(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: the frontmatter is not YAML, so its type is unknown: %w", shown, err)
		}
		kind, ok := autoMemoryKinds[strings.ToLower(front.Metadata.Type)]
		if !ok {
			if kind, ok = autoMemoryKinds[strings.ToLower(front.Type)]; !ok {
				kind = memory.Fact
			}
		}
		// The body is the memory, the Why and How lines with it: they are what
		// makes a rule something the next agent can judge rather than obey.
		// A note with no body still said something in its description.
		text := strings.Join(strings.Fields(body), " ")
		if text == "" {
			text = strings.Join(strings.Fields(front.Description), " ")
		}
		if text == "" {
			out = append(out, ruleFile{shown: shown, skip: "empty"})
			continue
		}
		// A user note is who the person is, which does not change at the next
		// repository; scoped to this one, every other project's recall missed it.
		scoped := project
		if strings.EqualFold(front.Metadata.Type, "user") || strings.EqualFold(front.Type, "user") {
			scoped = ""
		}
		out = append(out, ruleFile{shown: shown, project: scoped, items: []string{text}, kind: kind})
	}
	return out, nil
}

// splitAutoMemory separates a note's frontmatter from its body. The fences are
// whole "---" lines, so an empty block is still a block and a "----" rule in
// the body is not the end of one.
func splitAutoMemory(text string) (autoMemoryFront, string, error) {
	var front autoMemoryFront
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return front, text, nil
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			err := yaml.Unmarshal([]byte(strings.Join(lines[1:i], "\n")), &front)
			return front, strings.Join(lines[i+1:], "\n"), err
		}
	}
	return front, text, nil
}
