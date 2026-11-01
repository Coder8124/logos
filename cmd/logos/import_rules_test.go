package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

const shopClaudeMD = "# Shop\n\n## Conventions\n\n" +
	"- Commit messages are one sentence, present tense.\n" +
	"- Test names are sentences:\n  they state the property.\n\n" +
	"Run the migrations before the tests, or the fixtures fail to load.\n\n" +
	"```sh\nmake fixtures\n```\n\n" +
	"Before you stop:\n"

// standInRules is a repository with rule files in it and a home of its own, so
// ~/.claude is the test's and not the machine's. CLAUDE_CONFIG_DIR is cleared
// for the same reason: set on the machine, it would be read instead.
func standInRules(t *testing.T, files map[string]string) string {
	t.Helper()
	vaultDir := standIn(t, "shop")
	t.Setenv("LOGOS_EMBED", "off")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := os.MkdirAll(".git", 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		path := name
		if rest, ok := strings.CutPrefix(name, "~/"); ok {
			path = filepath.Join(os.Getenv("HOME"), rest)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return vaultDir
}

func importRules(t *testing.T, from string, extra ...string) string {
	t.Helper()
	return captureStdout(t, func() {
		if err := runImport(append([]string{"--from", from, "--yes"}, extra...)); err != nil {
			t.Fatalf("import --from %s: %v", from, err)
		}
	})
}

func pendingFrom(t *testing.T, source string) []memory.Memory {
	t.Helper()
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	pend, err := memory.Pending(ix.DB)
	if err != nil {
		t.Fatal(err)
	}
	var out []memory.Memory
	for _, m := range pend {
		if m.Source == source {
			out = append(out, m)
		}
	}
	return out
}

func texts(ms []memory.Memory) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Text)
	}
	slices.Sort(out)
	return out
}

// Most of a CLAUDE.md is convention, which belongs in CONTRIBUTING.md and not
// in memory. So nothing it says is known until someone accepts it: every rule
// arrives in the review queue, and recall sees none of them.
func TestImportingClaudeMDQueuesEachRuleForReviewAndRecallsNone(t *testing.T) {
	standInRules(t, map[string]string{"CLAUDE.md": shopClaudeMD})

	out := importRules(t, "claude-md")
	if !strings.Contains(out, "Imported 3, skipped 0 (already present), refused 0") {
		t.Errorf("the report does not count three rules:\n%s", out)
	}
	if !strings.Contains(out, "logos review") {
		t.Errorf("the report does not say where the proposals wait:\n%s", out)
	}
	want := []string{
		"Commit messages are one sentence, present tense.",
		"Run the migrations before the tests, or the fixtures fail to load.",
		"Test names are sentences: they state the property.",
	}
	got := pendingFrom(t, claudeMDSource)
	if !slices.Equal(texts(got), want) {
		t.Errorf("queued %q, want %q", texts(got), want)
	}
	for _, m := range got {
		if m.Project != "shop" || m.Confidence >= 0.85 {
			t.Errorf("a repo rule was queued as project %q, confidence %v", m.Project, m.Confidence)
		}
	}
	if all := importedMemoriesFrom(t, claudeMDSource); len(all) != 0 {
		t.Errorf("an imported rule is recallable before anyone accepted it: %+v", all)
	}
}

func TestImportingClaudeMDTwiceQueuesNothingTheSecondTime(t *testing.T) {
	standInRules(t, map[string]string{"CLAUDE.md": shopClaudeMD})
	importRules(t, "claude-md")
	if again := importRules(t, "claude-md"); !strings.Contains(again, "Imported 0, skipped 3 (already present)") {
		t.Errorf("a second import did not find all three already queued:\n%s", again)
	}
	if n := len(pendingFrom(t, claudeMDSource)); n != 3 {
		t.Errorf("two imports left %d proposals, want 3", n)
	}
}

// ~/.claude/CLAUDE.md applies to every repository, so its rules are global.
// AGENTS.md is read beside CLAUDE.md: it is the same file for other agents.
func TestHomeClaudeMDRulesAreGlobalAndAgentsMDIsReadToo(t *testing.T) {
	standInRules(t, map[string]string{
		"~/.claude/CLAUDE.md": "- Never push to main without asking first.\n",
		"AGENTS.md":           "- Run go vet before every commit.\n",
	})
	importRules(t, "claude-md")
	got := map[string]string{}
	for _, m := range pendingFrom(t, claudeMDSource) {
		got[m.Text] = m.Project
	}
	if p, ok := got["Never push to main without asking first."]; !ok || p != "" {
		t.Errorf("the home rule was queued as project %q (found %v), want global", p, ok)
	}
	if p, ok := got["Run go vet before every commit."]; !ok || p != "shop" {
		t.Errorf("the AGENTS.md rule was queued as project %q (found %v), want shop", p, ok)
	}
}

// Claude Code loads instructions from more places than CLAUDE.md: a personal
// CLAUDE.local.md, .claude/AGENTS.md, and every .md under .claude/rules and
// the user's rules folder, subfolders included. An import that read only
// CLAUDE.md left those rules out and called the rest everything.
func TestEveryFileClaudeCodeReadsInstructionsFromIsImported(t *testing.T) {
	standInRules(t, map[string]string{
		"CLAUDE.local.md":              "- Use the staging database for manual checks.\n",
		".claude/AGENTS.md":            "- Keep handlers free of SQL.\n",
		".claude/rules/go/errors.md":   "- Wrap errors with the operation that failed.\n",
		".claude/rules/notes.txt":      "- Not a rules file at all.\n",
		"~/.claude/rules/git.md":       "- Sign every release tag.\n",
		"~/.claude/rules/deep/rest.md": "- Return 404 for a missing resource.\n",
	})
	out := importRules(t, "claude-md")
	got := map[string]string{}
	for _, m := range pendingFrom(t, claudeMDSource) {
		got[m.Text] = m.Project
	}
	want := map[string]string{
		"Use the staging database for manual checks.": "shop",
		"Keep handlers free of SQL.":                  "shop",
		"Wrap errors with the operation that failed.": "shop",
		"Sign every release tag.":                     "",
		"Return 404 for a missing resource.":          "",
	}
	if !maps.Equal(got, want) {
		t.Errorf("queued %v, want %v\n%s", got, want, out)
	}
}

// With CLAUDE_CONFIG_DIR set, Claude Code reads the user's CLAUDE.md and rules
// from there and not from ~/.claude, so the import does the same.
func TestUserRulesAreReadFromClaudeConfigDirWhenItIsSet(t *testing.T) {
	standInRules(t, map[string]string{
		"~/.claude/CLAUDE.md": "- A rule Claude Code is not reading.\n",
	})
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	for name, body := range map[string]string{
		"CLAUDE.md":      "- Never push to main without asking first.\n",
		"rules/tests.md": "- Name each test as a sentence.\n",
	} {
		path := filepath.Join(config, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := importRules(t, "claude-md")
	want := []string{"Name each test as a sentence.", "Never push to main without asking first."}
	if got := texts(pendingFrom(t, claudeMDSource)); !slices.Equal(got, want) {
		t.Errorf("queued %q, want %q\n%s", got, want, out)
	}
}

// A team shares rules by linking a folder into .claude/rules, and Claude Code
// reads through the link. A link that points back up the tree must end the
// walk, not loop it.
func TestALinkedRulesFolderIsReadThroughAndALoopEnds(t *testing.T) {
	standInRules(t, map[string]string{
		".claude/rules/local.md": "- Keep handlers free of SQL.\n",
	})
	shared := t.TempDir()
	if err := os.WriteFile(filepath.Join(shared, "team.md"), []byte("- Review every migration twice.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(".claude", "rules", "shared")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..", filepath.Join(".claude", "rules", "up")); err != nil {
		t.Fatal(err)
	}
	out := importRules(t, "claude-md")
	want := []string{"Keep handlers free of SQL.", "Review every migration twice."}
	if got := texts(pendingFrom(t, claudeMDSource)); !slices.Equal(got, want) {
		t.Errorf("queued %q, want %q\n%s", got, want, out)
	}
}

// A file link that names itself cannot be resolved; it has nothing to read,
// like a dangling one. It ended the whole import with "too many levels of
// symbolic links", the repository's CLAUDE.md included.
func TestAFileLinkThatLoopsIsSkippedNotFatal(t *testing.T) {
	standInRules(t, map[string]string{
		"CLAUDE.md":              "- Keep handlers free of SQL.\n",
		".claude/rules/local.md": "- Review every migration twice.\n",
	})
	if err := os.Symlink("self.md", filepath.Join(".claude", "rules", "self.md")); err != nil {
		t.Fatal(err)
	}
	out := importRules(t, "claude-md")
	want := []string{"Keep handlers free of SQL.", "Review every migration twice."}
	if got := texts(pendingFrom(t, claudeMDSource)); !slices.Equal(got, want) {
		t.Errorf("queued %q, want %q\n%s", got, want, out)
	}
}

// Cursor keeps its rules in .mdc files with frontmatter, and in the older
// single .cursorrules. The frontmatter is Cursor's configuration, not a rule.
func TestCursorRulesAreQueuedFromMdcBodiesAndCursorrules(t *testing.T) {
	standInRules(t, map[string]string{
		".cursor/rules/style.mdc": "---\ndescription: house style\nglobs: \"*.go\"\nalwaysApply: true\n---\n\n- Wrap errors with the operation that failed.\n",
		".cursorrules":            "Prefer table-driven tests for parsers.\n",
	})
	out := importRules(t, "cursor-rules")
	want := []string{"Prefer table-driven tests for parsers.", "Wrap errors with the operation that failed."}
	if got := texts(pendingFrom(t, cursorRulesSource)); !slices.Equal(got, want) {
		t.Errorf("queued %q, want %q\n%s", got, want, out)
	}
}

func TestAnImportedRuleThatIsOnlyASecretIsRefusedAndCounted(t *testing.T) {
	const key = "sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGh"
	standInRules(t, map[string]string{"CLAUDE.md": "- " + key + "\n- Deploy with " + key + " from the shared vault.\n"})
	out := importRules(t, "claude-md")
	if !strings.Contains(out, "Imported 1") || !strings.Contains(out, "refused 1") {
		t.Errorf("a secret-only rule and a masked one were not reported as one imported, one refused:\n%s", out)
	}
	for _, m := range pendingFrom(t, claudeMDSource) {
		if strings.Contains(m.Text, key) {
			t.Errorf("the key reached the queue: %q", m.Text)
		}
	}
}

// The queue is vault state like any other: a proposal only the index knew
// would be lost to the next rebuild.
func TestAnImportedRuleIsInTheVaultBeforeTheIndex(t *testing.T) {
	vaultDir := standInRules(t, map[string]string{"CLAUDE.md": shopClaudeMD})
	importRules(t, "claude-md")
	raw, err := os.ReadFile(filepath.Join(vaultDir, memory.Dir, memory.PendingFile))
	if err != nil || !strings.Contains(string(raw), "Commit messages are one sentence") {
		t.Fatalf("the proposals are not in %s (err %v):\n%s", memory.PendingFile, err, raw)
	}
	if err := os.RemoveAll(filepath.Join(vaultDir, ".logos")); err != nil {
		t.Fatal(err)
	}
	if err := runIndex(false); err != nil {
		t.Fatal(err)
	}
	if n := len(pendingFrom(t, claudeMDSource)); n != 3 {
		t.Errorf("after rebuilding the index %d proposals came back, want 3", n)
	}
}

func TestADryRunRuleImportCountsAndWritesNothing(t *testing.T) {
	standInRules(t, map[string]string{"CLAUDE.md": shopClaudeMD})
	out := captureStdout(t, func() {
		if err := runImport([]string{"--from", "claude-md", "--dry-run"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "CLAUDE.md") || !strings.Contains(out, "3 proposals would be queued, 0 already in the queue or in memory. Nothing was written.") {
		t.Errorf("the dry run did not name the file and count:\n%s", out)
	}
	if n := len(pendingFrom(t, claudeMDSource)); n != 0 {
		t.Errorf("a dry run queued %d", n)
	}
}

func importedMemoriesFrom(t *testing.T, source string) []memory.Memory {
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
		if m.Source == source {
			out = append(out, m)
		}
	}
	return out
}

// A rejection is a decision. A rules file is read again on every import, so a
// rule the user threw out of the queue was back in it the next time, and the
// queue became the place where the same conventions were rejected weekly.
func TestARejectedImportedRuleIsNotQueuedAgain(t *testing.T) {
	standInRules(t, map[string]string{"CLAUDE.md": shopClaudeMD})
	importRules(t, "claude-md")
	queued := pendingFrom(t, claudeMDSource)
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Reject(ix.DB, queued[0].ID); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	again := importRules(t, "claude-md")
	if !strings.Contains(again, "Imported 0, skipped 3") {
		t.Errorf("a re-import after a rejection did not skip all three:\n%s", again)
	}
	if !strings.Contains(again, "1 you rejected or forgot before") {
		t.Errorf("the re-import did not say it left the rejected rule out:\n%s", again)
	}
	if n := len(pendingFrom(t, claudeMDSource)); n != 2 {
		t.Errorf("after one rejection and a re-import %d proposals wait, want 2", n)
	}
}

// The undo the import report prints, run on an import of one rule.
func TestForgettingOneImportedRuleBySourceSaysOneMemory(t *testing.T) {
	standInRules(t, map[string]string{".cursorrules": "Prefer table-driven tests for parsers.\n"})
	importRules(t, "cursor-rules")
	out := captureStdout(t, func() {
		if err := memoryCmd([]string{"forget", "--source", cursorRulesSource}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "forgot 1 memory learned from cursor-rules.") {
		t.Errorf("forgetting one memory said %q", out)
	}
}

// Run from home, the repository's .claude/CLAUDE.md and the global one are the
// same file. Kept as the repository's, the rules a person wrote for every
// project were recalled in one project named after their home directory.
func TestHomeClaudeMDStaysGlobalWhenTheImportRunsFromHome(t *testing.T) {
	standInRules(t, map[string]string{"~/.claude/CLAUDE.md": "- Never push to main without asking first.\n"})
	if err := os.Chdir(os.Getenv("HOME")); err != nil {
		t.Fatal(err)
	}
	importRules(t, "claude-md")
	got := pendingFrom(t, claudeMDSource)
	if len(got) != 1 || got[0].Project != "" {
		t.Errorf("the home rule was queued as %+v, want once and global", got)
	}
}

// A rule the dedup folded into a memory worded a little differently is in the
// timeline under its own words and in no row. That is not a rejection, and the
// report used to tell the user they had rejected it.
func TestARuleFoldedIntoAnExistingMemoryIsNotReportedAsRejected(t *testing.T) {
	standInRules(t, map[string]string{"CLAUDE.md": "- commit messages are one sentence, present tense.\n"})
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Store(ix.DB, nil, "", &memory.Memory{Text: "Commit messages are one sentence, present tense.", Kind: memory.Preference, Project: "shop"}); err != nil {
		t.Fatal(err)
	}
	ix.Close()
	importRules(t, "claude-md")
	if again := importRules(t, "claude-md"); strings.Contains(again, "rejected or forgot") {
		t.Errorf("a rule nobody rejected was reported as rejected:\n%s", again)
	}
}

// The dry run and the prompt are the user's preview; counting rules the real
// run will leave out made the preview a promise the import did not keep.
func TestADryRunDoesNotCountARuleTheUserRejected(t *testing.T) {
	standInRules(t, map[string]string{"CLAUDE.md": shopClaudeMD})
	importRules(t, "claude-md")
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Reject(ix.DB, pendingFrom(t, claudeMDSource)[0].ID); err != nil {
		t.Fatal(err)
	}
	ix.Close()
	out := captureStdout(t, func() {
		if err := runImport([]string{"--from", "claude-md", "--dry-run"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "0 proposals would be queued") || !strings.Contains(out, "1 you rejected or forgot before") {
		t.Errorf("the dry run counted a rejected rule:\n%s", out)
	}
}

// A note hidden from the rendered file is hidden for a reason; its middle lines
// were queued as a rule.
func TestAMultiLineHTMLCommentIsNotARule(t *testing.T) {
	got := ruleItems("- Keep the tests fast.\n<!--\nTODO: rewrite this section\n-->\n- Run vet.\n")
	want := []string{"Keep the tests fast.", "Run vet."}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// AGENTS.md is often a symlink to CLAUDE.md. Read under both names, every rule
// was found twice and half came back "already present" on a first import.
func TestAnAgentsMDThatLinksToClaudeMDIsReadOnce(t *testing.T) {
	standInRules(t, map[string]string{"CLAUDE.md": shopClaudeMD})
	if err := os.Symlink("CLAUDE.md", "AGENTS.md"); err != nil {
		t.Fatal(err)
	}
	if out := importRules(t, "claude-md"); !strings.Contains(out, "Imported 3, skipped 0") {
		t.Errorf("a linked AGENTS.md was read as a second file:\n%s", out)
	}
}

func TestNothingToImportFromCursorDoesNotNameClaudesFolder(t *testing.T) {
	standInRules(t, nil)
	out := importRules(t, "cursor-rules")
	if !strings.Contains(out, "Nothing to import") || strings.Contains(out, ".claude") {
		t.Errorf("the empty cursor import named a place it did not look:\n%s", out)
	}
}

// What was rejected is read from the index, and the index is a cache. With it
// deleted and not yet rebuilt, the import read an empty timeline and queued
// every rejected rule again.
func TestARejectedRuleStaysOutWhenTheIndexWasDeletedAndNotRebuilt(t *testing.T) {
	vaultDir := standInRules(t, map[string]string{"CLAUDE.md": shopClaudeMD})
	importRules(t, "claude-md")
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Reject(ix.DB, pendingFrom(t, claudeMDSource)[0].ID); err != nil {
		t.Fatal(err)
	}
	ix.Close()
	if err := os.RemoveAll(filepath.Join(vaultDir, ".logos")); err != nil {
		t.Fatal(err)
	}
	if out := importRules(t, "claude-md"); !strings.Contains(out, "Imported 0, skipped 3") {
		t.Errorf("an import on a deleted index did not find what was rejected and queued:\n%s", out)
	}
	if n := len(pendingFrom(t, claudeMDSource)); n != 2 {
		t.Errorf("%d proposals wait, want 2", n)
	}
}

// The dry run counted every rule it did not drop as rejected, so a second look
// at a file already imported promised proposals the import then skipped.
func TestADryRunAfterAnImportPromisesNothingNew(t *testing.T) {
	standInRules(t, map[string]string{"CLAUDE.md": shopClaudeMD})
	importRules(t, "claude-md")
	out := captureStdout(t, func() {
		if err := runImport([]string{"--from", "claude-md", "--dry-run"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "0 proposals would be queued, 3 already in the queue or in memory") {
		t.Errorf("the dry run promised what the import would skip:\n%s", out)
	}
}

// The rebuild restores working notes from the vault; its report went nowhere,
// so an import repaired the index and never said what it brought back.
func TestAnImportThatRebuildsTheIndexSaysWhatTheRebuildRestored(t *testing.T) {
	vaultDir := standInRules(t, map[string]string{"CLAUDE.md": shopClaudeMD})
	if err := runNote([]string{"shop", "priced the extruded option"}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(vaultDir, ".logos")); err != nil {
		t.Fatal(err)
	}
	errOut := captureStderr(t, func() { importRules(t, "claude-md") })
	if !strings.Contains(errOut, "restored 1 uncommitted working note") {
		t.Errorf("the rebuild's repairs were not reported:\n%s", errOut)
	}
}

// With nothing new to queue, every held rule was stored again: a reinforced
// line per rule in the vault's log, from a re-import the user meant as a look.
func TestReImportingRulesWritesNothingToTheVault(t *testing.T) {
	vaultDir := standInRules(t, map[string]string{"CLAUDE.md": shopClaudeMD})
	importRules(t, "claude-md")
	logPath := filepath.Join(vaultDir, "memories", "log.md")
	before, _ := os.ReadFile(logPath)
	if out := importRules(t, "claude-md"); !strings.Contains(out, "Imported 0, skipped 3") {
		t.Errorf("the re-import did not count what it skipped:\n%s", out)
	}
	if after, _ := os.ReadFile(logPath); string(after) != string(before) {
		t.Errorf("a re-import of the same file wrote to the vault's log:\n%s", after[len(before):])
	}
}

func TestThePreviewSampleLeavesOutARejectedRule(t *testing.T) {
	standInRules(t, map[string]string{"CLAUDE.md": shopClaudeMD})
	importRules(t, "claude-md")
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	first := pendingFrom(t, claudeMDSource)[0]
	if err := memory.Reject(ix.DB, first.ID); err != nil {
		t.Fatal(err)
	}
	ix.Close()
	if err := os.WriteFile("AGENTS.md", []byte("- Deploys go through the release branch.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := runImport([]string{"--from", "claude-md", "--dry-run"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "  - "+first.Text) || !strings.Contains(out, "  - Deploys go through the release branch.") {
		t.Errorf("the sample showed a rule the import leaves out, or not the one it queues:\n%s", out)
	}
}

func TestACommentInsideALineIsNotPartOfTheRule(t *testing.T) {
	got := ruleItems("- Always run make <!-- drop once\nCI is fixed -->\n<!-- note --> Use tabs.\n- Run vet. <!-- why --> Always.\n")
	want := []string{"Always run make", "Use tabs.", "Run vet. Always."}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
