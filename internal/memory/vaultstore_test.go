package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The claim under test is the project's second principle, applied to the half
// of the system where it used to be false: delete .logos, reindex, get the same
// state back.

func vaultDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })
	return db, dir
}

// The demo, executed. Everything the assistant knows about you survives losing
// the database, because the database was never where it lived.
func TestMemoriesSurviveLosingTheDatabase(t *testing.T) {
	db, dir := vaultDB(t)

	for _, m := range []Memory{
		{Text: "I prefer written proposals over meetings", Kind: Preference, Source: "manual"},
		{Text: "Tomas runs manufacturing operations", Kind: Person, Source: "manual"},
		{Text: "Our contract manufacturer is Pegatron, in the Suzhou plant", Kind: Fact, Source: "mcp"},
		{Text: "Shipping the glasses in November", Kind: Context, Source: "conversation"},
	} {
		if _, err := Store(db, nil, "", &m); err != nil {
			t.Fatal(err)
		}
	}

	// The wipe. A fresh database, as if .logos had been deleted.
	wiped := testDB(t)
	n, _, err := Import(wiped, nil, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("want 4 memories restored, got %d", n)
	}

	all, err := All(wiped)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Kind{}
	for _, m := range all {
		got[m.Text] = m.Kind
	}
	for text, kind := range map[string]Kind{
		"I prefer written proposals over meetings":                   Preference,
		"Tomas runs manufacturing operations":                        Person,
		"Our contract manufacturer is Pegatron, in the Suzhou plant": Fact,
		"Shipping the glasses in November":                           Context,
	} {
		if got[text] != kind {
			t.Errorf("lost %q (want kind %s, got %q)", text, kind, got[text])
		}
	}
}

// Identity has to survive too. memory_log references ids, and so does anything
// the user has cited — restoring the same fact under a new number would break
// the history that makes the store auditable.
func TestIdsSurviveTheRoundTrip(t *testing.T) {
	db, dir := vaultDB(t)

	m := Memory{Text: "The launch price is $249", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	original := m.ID

	wiped := testDB(t)
	if _, _, err := Import(wiped, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	all, _ := All(wiped)
	if len(all) != 1 {
		t.Fatalf("want 1 memory, got %d", len(all))
	}
	if all[0].ID != original {
		t.Errorf("id changed across the wipe: was %d, now %d", original, all[0].ID)
	}
}

// The file is the record, so editing it has to be how you correct a fact —
// otherwise "markdown is truth" is decoration. Under A2 that file is the
// memory's own file under memories/fact/, not the generated memories/fact.md
// digest; editing the digest instead is exactly what
// TestAHandEditToTheKindDigestIsNeverSilentlyOverwritten proves is refused.
func TestEditingTheFileCorrectsTheMemory(t *testing.T) {
	db, dir := vaultDB(t)

	m := Memory{Text: "The launch price is $199", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	path := memoryFilePath(dir, Fact, m.Uid)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), "$199", "$249", 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Import(db, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	all, _ := All(db)
	if len(all) != 1 || !strings.Contains(all[0].Text, "$249") {
		t.Errorf("the edit did not stick: %+v", all)
	}
	if all[0].ID != m.ID {
		t.Errorf("correcting a fact should not renumber it: was %d, now %d", m.ID, all[0].ID)
	}
}

// Deleting a line forgets it. The file has to be able to remove as well as add,
// or it is an append-only log wearing a document's clothes. Under A2 the file
// that means is the memory's own file under memories/person/ — deleting it is
// the moral equivalent of what deleting a line from the old one-file-per-kind
// vault used to mean.
func TestDeletingALineForgetsIt(t *testing.T) {
	db, dir := vaultDB(t)

	keep := Memory{Text: "Tomas runs manufacturing", Kind: Person, Source: "manual"}
	drop := Memory{Text: "Sarah is the CFO", Kind: Person, Source: "manual"}
	for _, m := range []*Memory{&keep, &drop} {
		if _, err := Store(db, nil, "", m); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Remove(memoryFilePath(dir, Person, drop.Uid)); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Import(db, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	all, _ := All(db)
	for _, m := range all {
		if strings.Contains(m.Text, "Sarah") {
			t.Error("deleting the line should have forgotten the memory")
		}
	}
	if len(all) != 1 {
		t.Errorf("want the other memory kept, got %d", len(all))
	}
}

// Forgetting through the API must reach the file, or the next reindex would
// resurrect it — the worst possible bug in a system people delete things from.
func TestForgettingReachesTheFile(t *testing.T) {
	db, dir := vaultDB(t)

	m := Memory{Text: "Something regrettable", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	if err := Forget(db, m.ID); err != nil {
		t.Fatal(err)
	}

	if raw, err := os.ReadFile(filepath.Join(dir, Dir, "fact.md")); err == nil {
		if strings.Contains(string(raw), "regrettable") {
			t.Error("a forgotten memory is still in the vault file")
		}
	}
	wiped := testDB(t)
	Import(wiped, nil, "", dir)
	all, _ := All(wiped)
	for _, got := range all {
		if strings.Contains(got.Text, "regrettable") {
			t.Error("reindexing resurrected a forgotten memory")
		}
	}
}

// A line typed by hand, with no bookkeeping comment, is a valid memory. Telling
// someone the file is truth and then ignoring what they write in it would be a
// lie.
func TestHandWrittenLinesAreAccepted(t *testing.T) {
	db, dir := vaultDB(t)

	os.MkdirAll(filepath.Join(dir, Dir), 0o755)
	os.WriteFile(filepath.Join(dir, Dir, "preference.md"),
		[]byte("---\ntype: memory-store\nkind: preference\n---\n\n- I like my coffee before any meeting\n"), 0o644)

	n, _, err := Import(db, nil, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want the hand-written line imported, got %d", n)
	}
	all, _ := All(db)
	if len(all) != 1 || !strings.Contains(all[0].Text, "coffee") {
		t.Errorf("hand-written memory missing: %+v", all)
	}
	if all[0].ID == 0 {
		t.Error("an imported memory needs an id")
	}
}

// Notepad saves a file with a byte-order mark, and TrimSpace does not count
// U+FEFF as space, so the first line of a kind file typed with no frontmatter
// began with an invisible rune instead of "- " and was never read: the memory
// typed first was the one that silently never became a memory.
func TestAMemoryFileSavedWithAByteOrderMarkKeepsItsFirstLine(t *testing.T) {
	db, dir := vaultDB(t)

	os.MkdirAll(filepath.Join(dir, Dir), 0o755)
	os.WriteFile(filepath.Join(dir, Dir, "preference.md"),
		[]byte("\ufeff- I like my coffee before any meeting\r\n- standups are at 09:15\r\n"), 0o644)

	n, _, err := Import(db, nil, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("want both typed lines imported, got %d", n)
	}
	all, _ := All(db)
	var texts []string
	for _, m := range all {
		texts = append(texts, m.Text)
	}
	if !strings.Contains(strings.Join(texts, "|"), "coffee") {
		t.Errorf("the line after the byte-order mark is missing: %q", texts)
	}
}

// A vault that has never exported must not be read as "the user deleted
// everything". This is the dangerous direction: importing an empty vault over a
// full database would destroy exactly what the feature protects.
func TestAnEmptyVaultDoesNotWipeTheStore(t *testing.T) {
	db, dir := vaultDB(t)

	m := Memory{Text: "Precious and unexported", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	// Simulate a store that predates the vault format.
	os.RemoveAll(filepath.Join(dir, Dir))

	if _, _, err := Import(db, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	all, _ := All(db)
	if len(all) != 1 {
		t.Fatalf("importing an empty vault destroyed the store: %d left", len(all))
	}
	// And it should have written the file out, so the next wipe is survivable.
	if _, err := os.ReadFile(filepath.Join(dir, Dir, "fact.md")); err != nil {
		t.Errorf("an unexported store should be exported on first import: %v", err)
	}
}

// Superseded memories are history, not knowledge. They stay in the log and out
// of the file the user reads.
func TestSupersededMemoriesAreNotExported(t *testing.T) {
	db, dir := vaultDB(t)

	old := Memory{Text: "Retail is $199", Kind: Fact, Source: "manual"}
	Store(db, nil, "", &old)
	current := Memory{Text: "Retail is locked at $249 for launch", Kind: Fact, Source: "manual"}
	Store(db, nil, "", &current)

	db.Exec("UPDATE memories SET superseded = 1, superseded_by = ? WHERE id = ?", current.ID, old.ID)
	if err := Export(db, dir); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, Dir, "fact.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "$199") {
		t.Errorf("a superseded value was written into the vault:\n%s", raw)
	}
	if !strings.Contains(string(raw), "$249") {
		t.Errorf("the current value is missing:\n%s", raw)
	}
}

// The rendered file has to be pleasant to open, because the pitch is that you
// can read it.
func TestTheFileReadsAsADocument(t *testing.T) {
	db, dir := vaultDB(t)
	m := Memory{Text: "I prefer written proposals over meetings", Kind: Preference, Source: "manual"}
	Store(db, nil, "", &m)

	raw, err := os.ReadFile(filepath.Join(dir, Dir, "preference.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		"type: memory-store",
		"How you like things done.",
		"- I prefer written proposals over meetings",
		"<!-- logos id=",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q from:\n%s", want, text)
		}
	}
	// The bookkeeping must be invisible when rendered, or the file is a data
	// dump rather than something a person would read.
	if strings.Contains(text, "\nid=") || strings.Contains(text, "\nconf=") {
		t.Errorf("metadata leaked outside its comment:\n%s", text)
	}
}

// Agent has to make the same round trip as project and source — written into
// the bookkeeping comment, read back on import — so who learned a fact
// survives an `rm -rf .logos`, not just the fact itself.
func TestAgentSurvivesExportAndImport(t *testing.T) {
	db, dir := vaultDB(t)
	if _, err := Store(db, nil, "", &Memory{
		Text: "the vault path is configurable", Kind: Fact, Source: "mcp", Agent: "cursor",
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, Dir, "fact.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "agent=cursor") {
		t.Errorf("agent metadata missing from the exported file:\n%s", raw)
	}

	wiped := testDB(t)
	if _, _, err := Import(wiped, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	all, err := All(wiped)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Agent != "cursor" {
		t.Fatalf("agent did not survive the round trip: %+v", all)
	}
}

// A file written before this field existed has no `agent=` in its bookkeeping
// comment at all. Importing it must not choke — it is exactly the pre-migration
// case, at the vault-file layer instead of the database layer.
func TestOldVaultFileWithNoAgentMetadataImportsCleanly(t *testing.T) {
	db, dir := vaultDB(t)
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	old := "---\ntype: memory-store\nkind: fact\ncount: 1\n---\n\n" +
		"- a fact written before agents were tracked <!-- logos id=1 conf=0.70 sal=0.50 src=manual created=2025-01-01 uses=0 -->\n"
	if err := os.WriteFile(filepath.Join(dir, Dir, "fact.md"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Import(db, nil, "", dir); err != nil {
		t.Fatalf("importing a pre-agent vault file failed: %v", err)
	}
	all, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("want 1 memory, got %d", len(all))
	}
	if all[0].Agent != "" {
		t.Errorf("a file with no agent= should import with an empty agent, got %q", all[0].Agent)
	}
}

// A memory is free text a person or an agent wrote, and it may quote logos's
// own bookkeeping — a note about the vault format, a pasted line from a store
// file. logos's comment is always appended last, so splitting the line at the
// first "<!--" hands the user's own text to the metadata parser and drops it.
//
// The loss needs a second, unrelated write to show: the first store renders the
// file correctly, and only the next one parses it back before rewriting. That
// rewrite is what makes this silent and permanent — the truncated form is then
// what the vault holds, so a rebuild is faithful to corrupt data.
func TestAMemoryQuotingLogosOwnCommentIsNotTruncatedByTheNextWrite(t *testing.T) {
	db, dir := vaultDB(t)

	const quoted = "the deploy script uses <!-- logos id=999 --> as a marker"
	first := Memory{Text: quoted, Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &first); err != nil {
		t.Fatal(err)
	}
	second := Memory{Text: "second unrelated fact", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &second); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, Dir, "fact.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), quoted) {
		t.Errorf("the vault lost the memory's text on the second write:\n%s", raw)
	}

	wiped := testDB(t)
	if _, _, err := Import(wiped, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	all, err := All(wiped)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.ID == first.ID && m.Text != quoted {
			t.Errorf("rebuilt memory #%d as %q, want %q", m.ID, m.Text, quoted)
		}
	}
}

// Recall stamps last_used and raises uses, and only uses ever reached the file.
// So `rm -rf .logos && logos index` rolled the ranking signal back to whatever
// was last flushed and restarted decay from `created` — invariant 1 broken for
// the signal ranking trusts, rather than for the text.
func TestTheUsageSignalSurvivesDeletingTheIndex(t *testing.T) {
	db, dir := vaultDB(t)
	m := Memory{Text: "the release train ships on Thursdays", Kind: Fact, Source: "manual"}
	r, err := Store(db, nil, "", &m)
	if err != nil {
		t.Fatal(err)
	}
	// What a handful of recalls over the past few days leaves behind.
	used := time.Now().Add(-36 * time.Hour).Unix()
	if _, err := db.Exec("UPDATE memories SET last_used = ?, uses = 3 WHERE id = ?", used, r.ID); err != nil {
		t.Fatal(err)
	}
	// Under A2 the memory's own file under memories/fact/ is what readKindFiles
	// treats as already-represented truth, so exporting only the digest here
	// would leave the stale pre-update per-memory file as the thing Import
	// adopts, and the newer uses/last_used would never reach it. A real usage
	// update calls exportMemoryFile for the same reason; this is that call.
	if err := exportMemoryFile(db, Fact, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := ExportKind(db, dir, Fact); err != nil {
		t.Fatal(err)
	}

	wiped := testDB(t)
	if _, _, err := Import(wiped, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	all, err := All(wiped)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("want 1 memory restored, got %d", len(all))
	}
	if all[0].Uses != 3 {
		t.Errorf("use count came back as %d, not 3", all[0].Uses)
	}
	if all[0].LastUsed != used {
		t.Errorf("last_used came back as %d, not %d — decay restarts from created", all[0].LastUsed, used)
	}
}

// A stamp says "this file's bytes are ones this handle wrote, so a plain
// write of this kind is safe to overwrite it". SetVault forgot that claim on
// unbind but kept it on a rebind, so a handle moved to a second vault carried
// claims about the first. When the second vault's digest had the same bytes —
// a copied vault — the next write trusted a stamp describing the wrong vault
// and skipped the refusal flushLocked's guard exists to give instead.
//
// Under A2 a real copied vault carries every memory's own file under
// memories/fact/, not only the generated digest — that per-memory file is
// truth now, and the digest is rebuilt from the database, never adopted from
// on-disk bytes by a plain Store. So what this test copies has to be the
// whole per-memory directory, the way an actual vault copy (rsync, Obsidian
// Sync, a git checkout) would; copying only the digest, as the pre-A2 version
// of this test did, has no file left for `logos index` to recover the memory
// from and is not the scenario SetVault's stamp drop protects against.
func TestRebindingToAnotherVaultDoesNotTrustTheFirstVaultsStamps(t *testing.T) {
	db, first := vaultDB(t)
	m := Memory{Text: "The staging cluster has no rollback", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	second := t.TempDir()
	if err := os.CopyFS(filepath.Join(second, Dir), os.DirFS(filepath.Join(first, Dir))); err != nil {
		t.Fatal(err)
	}
	// The handle's rows are not the second vault's: it has never read it.
	if _, err := db.Exec(`DELETE FROM memories`); err != nil {
		t.Fatal(err)
	}

	SetVault(db, second)
	next := Memory{Text: "Deploys go out on Tuesdays", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &next); err != nil {
		t.Fatal(err)
	}

	// The plain Store above does not recover the staging-cluster memory — a
	// stale stamp only ever gated a refusal, never an adoption, and dropping
	// it on rebind correctly leaves ExportKind to rewrite the digest from
	// rows the handle actually has. What matters is that it rewrites rather
	// than refuses (a stale stamp wrongly trusted would instead error out, or
	// worse, silently skip writing this handle's own new memory), and that
	// the old memory's own file — the thing that actually carried it across
	// the copy — survived untouched, ready for `logos index` to bring it back.
	got, err := os.ReadFile(filepath.Join(second, Dir, "fact.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "Deploys go out on Tuesdays") {
		t.Errorf("the handle's own new memory is missing from the rewritten digest:\n%s", got)
	}
	oldFile := memoryFilePath(second, Fact, m.Uid)
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatalf("the copied memory's own file did not survive the rebind: %v", err)
	}

	if _, _, err := Import(db, nil, "", second); err != nil {
		t.Fatal(err)
	}
	if !remembers(t, db, m.Text) {
		t.Error("logos index did not recover the copied vault's own memory from its per-memory file")
	}
}

// A line typed by hand has no id comment, and Import adopts it without writing
// one back, so every later pass reads the same bare line. Each used to insert
// it again, and the unique fingerprint refused the second — failing every
// `logos index`, and every store through its reconcile, from then on.
func TestALineTypedByHandSurvivesEveryLaterImportAndStore(t *testing.T) {
	db, dir := vaultDB(t)
	path := filepath.Join(dir, Dir, "fact.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\ntype: memory-store\nkind: fact\n---\n\n- Typed by hand in Obsidian\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for i := range 2 {
		if _, _, err := Import(db, nil, "", dir); err != nil {
			t.Fatalf("import %d: %v", i+1, err)
		}
	}
	m := Memory{Text: "Stored after the hand-typed line", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatalf("store after a hand-typed line: %v", err)
	}
	all, _ := All(db)
	if len(all) != 2 {
		t.Errorf("want the typed line and the stored memory, got %+v", all)
	}
}

// The person typed, into the active file, the text of a memory still waiting
// for review. That line cannot become a second memory — the text is taken —
// and it used to fail every store's reconcile until somebody edited the file,
// so one stray line stopped every agent remembering anything.
func TestALineRepeatingAPendingMemoryDoesNotBlockLaterStores(t *testing.T) {
	db, dir := vaultDB(t)
	pending := Memory{Text: "The billing service is owned by payments", Kind: Fact, Source: "mcp", Quarantined: true}
	if _, err := Store(db, nil, "", &pending); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, Dir, "fact.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\ntype: memory-store\nkind: fact\n---\n\n- "+pending.Text+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, e, err := ImportEdits(db, nil, "", dir)
	if err != nil {
		t.Fatalf("index over the line: %v", err)
	}
	if len(e.Held) != 1 || !strings.Contains(e.Held[0], fmt.Sprintf("#%d", pending.ID)) {
		t.Errorf("index did not name the line it left alone and memory #%d: %q", pending.ID, e.Held)
	}
	later := Memory{Text: "Invoices are generated nightly", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &later); err != nil {
		t.Fatalf("a store after the line: %v", err)
	}
	if !isQueued(db, pending.ID) {
		t.Error("the line accepted the memory waiting for review; only review does that")
	}
}

// Under A2 there is no shared flat file to retype a line into — the thing a
// move really is now is the memory's own file carried from memories/person/
// to memories/fact/, same uid, same id comment. readKindFiles concatenates
// every kind's per-memory files before parsing, so which kind a memory's row
// gets comes from which directory holds its file when `logos index` reads
// them — not from a comment that says so.
//
// The other half of the old test's claim no longer holds, and this version
// says why instead of silently dropping it: a plain Store never rescans
// memories/<kind>/ for a move someone made by hand (exportMemoryFile only
// touches the id it was just given — see its own comment on why a per-Store
// rescan is the quadratic trap this package exists to avoid), so the memory
// keeps its old kind until an actual `logos index` notices the file is gone
// from one directory and present in the other. Either way the memory survives
// with its project intact; only an index changes which kind it ends up in.
func TestALineMovedToAnotherKindsFileWithoutItsCommentKeepsItsMemory(t *testing.T) {
	for _, viaIndex := range []bool{true, false} {
		db, dir := vaultDB(t)
		moved := Memory{Text: "Ana reviews every schema change", Kind: Person, Project: "shop", Source: "manual"}
		if _, err := Store(db, nil, "", &moved); err != nil {
			t.Fatal(err)
		}

		oldPath := memoryFilePath(dir, Person, moved.Uid)
		raw, err := os.ReadFile(oldPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(oldPath); err != nil {
			t.Fatal(err)
		}
		newPath := memoryFilePath(dir, Fact, moved.Uid)
		if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(newPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}

		if viaIndex {
			if _, _, err := Import(db, nil, "", dir); err != nil {
				t.Fatal(err)
			}
		} else {
			for _, m := range []Memory{
				{Text: "Bo runs the deploys", Kind: Person, Source: "manual"},
				{Text: "Deploys happen on Tuesdays", Kind: Fact, Source: "manual"},
			} {
				if _, err := Store(db, nil, "", &m); err != nil {
					t.Fatal(err)
				}
			}
		}

		var kind, project string
		if err := db.QueryRow("SELECT kind, project FROM memories WHERE id = ? AND superseded = 0", moved.ID).Scan(&kind, &project); err != nil {
			t.Fatalf("index=%v: memory #%d is gone after the move: %v", viaIndex, moved.ID, err)
		}
		if project != "shop" {
			t.Errorf("index=%v: memory #%d lost its project, got %q", viaIndex, moved.ID, project)
		}
		wantKind := Person
		if viaIndex {
			wantKind = Fact
		}
		if kind != string(wantKind) {
			t.Errorf("index=%v: memory #%d is kind %q, want %q", viaIndex, moved.ID, kind, wantKind)
		}

		if err := Export(db, dir); err != nil {
			t.Fatal(err)
		}
		factRaw, _ := os.ReadFile(filepath.Join(dir, Dir, "fact.md"))
		personRaw, _ := os.ReadFile(filepath.Join(dir, Dir, "person.md"))
		if viaIndex {
			if !strings.Contains(string(factRaw), moved.Text) {
				t.Errorf("index=%v: moved memory missing from fact.md:\n%s", viaIndex, factRaw)
			}
			if strings.Contains(string(personRaw), moved.Text) {
				t.Errorf("index=%v: moved memory still in person.md:\n%s", viaIndex, personRaw)
			}
		} else {
			if !strings.Contains(string(personRaw), moved.Text) {
				t.Errorf("index=%v: memory missing from person.md before any reindex:\n%s", viaIndex, personRaw)
			}
			if strings.Contains(string(factRaw), moved.Text) {
				t.Errorf("index=%v: a plain Store moved the memory into fact.md on its own:\n%s", viaIndex, factRaw)
			}
		}
	}
}

// A hand edit to memories/fact.md — the generated digest — must never be
// silently thrown away by the next Store of that kind. flushLocked compares
// the file's hash against what it last wrote; a mismatch means someone other
// than logos touched the digest, and the only acceptable response is to
// refuse and name the memory's own file as the place the edit belongs, not to
// quietly re-render over it. This is the plain-Store path only: `logos index`
// (Import) is documented in flushLocked and readKindFiles as the
// unconditional override that always wins over a hand edit to the digest.
func TestAHandEditToTheKindDigestIsNeverSilentlyOverwritten(t *testing.T) {
	db, dir := vaultDB(t)

	first := Memory{Text: "the contract manufacturer is Pegatron", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &first); err != nil {
		t.Fatal(err)
	}

	digestPath := filepath.Join(dir, Dir, "fact.md")
	handEdited := "a line a person typed directly into the generated digest\n"
	if err := os.WriteFile(digestPath, []byte(handEdited), 0o644); err != nil {
		t.Fatal(err)
	}

	second := Memory{Text: "the BOM target is $38", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &second); err == nil {
		t.Fatal("Store overwrote a hand edit to the generated digest without complaint")
	} else if !strings.Contains(err.Error(), "fact.md") || !strings.Contains(err.Error(), "memories/fact/") {
		t.Errorf("the refusal does not name both the digest and the memory's own file to edit instead: %v", err)
	}

	// The refusal is about the digest, not the memory: the new fact is still
	// durable on its own, in the cache and in its own file, even though the
	// kind's combined view could not be refreshed.
	all, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range all {
		if m.Text == second.Text {
			found = true
		}
	}
	if !found {
		t.Error("the new memory is missing from the cache after the digest refusal")
	}
	matches, err := filepath.Glob(filepath.Join(dir, Dir, "fact", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("want 2 per-memory fact files (one per Store call) despite the refused digest, got %d: %v", len(matches), matches)
	}

	// And the hand edit itself is untouched — the refusal happened before any
	// write to this file, not after one that then got reverted.
	after, err := os.ReadFile(digestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != handEdited {
		t.Errorf("the hand-edited digest changed despite the refusal:\nwant:\n%s\ngot:\n%s", handEdited, after)
	}
}

// TestAHandEditToAMemoryFileIsNeverSilentlyOverwritten proves exportMemoryFile
// has the same hand-edit guard writeMemoryFile's sibling flushLocked already
// had for the kind digest — before this test existed, a reinforcement or
// correction of the memory just overwrote a person's edit to its own file
// with no warning at all.
func TestAHandEditToAMemoryFileIsNeverSilentlyOverwritten(t *testing.T) {
	db, dir := vaultDB(t)

	m := Memory{Text: "the contract manufacturer is Pegatron", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	path := memoryFilePath(dir, Fact, m.Uid)
	handEdited := "a line a person typed directly into this memory's own file\n"
	if err := os.WriteFile(path, []byte(handEdited), 0o644); err != nil {
		t.Fatal(err)
	}

	// Storing the same text again hits insertLocked's exact-text duplicate
	// path, which bumps uses and (since the Finding #2 fix) now tries to
	// export the memory's file to carry that bump into the vault.
	again := Memory{Text: m.Text, Kind: Fact, Source: "manual"}
	_, err := Store(db, nil, "", &again)
	if err == nil {
		t.Fatal("Store overwrote a hand edit to the memory's own file without complaint")
	} else if !strings.Contains(err.Error(), m.Uid) {
		t.Errorf("the refusal does not name the memory file to edit instead: %v", err)
	}

	// The refusal is about the file, not the row: the reinforcement still
	// landed in the cache even though the per-memory file could not be
	// refreshed to match it.
	all, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Uses < 1 {
		t.Errorf("want the reinforcement to still land in the cache despite the refused file write: %+v", all)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != handEdited {
		t.Errorf("the hand-edited memory file changed despite the refusal:\nwant:\n%s\ngot:\n%s", handEdited, after)
	}
}

// TestAQuarantinedLineNeverGetsAFileDuringMigration proves readKindFiles does
// not pre-write a per-memory file for a hand-typed legacy line until upsert
// has actually accepted it. Before this fix, a quarantined memory's text
// sitting as a plain line in the old flat file got its own file written
// during migration regardless — and since upsert goes on to reject that exact
// line as heldLine (the text already belongs to memory #id, awaiting
// review), the result was a file on disk for a uid no row in the index ever
// held.
func TestAQuarantinedLineNeverGetsAFileDuringMigration(t *testing.T) {
	db, dir := vaultDB(t)

	text := "the launch slips to March"
	// Seeded directly, bypassing Store, so it lands quarantined with a known
	// fingerprint the way a real contested proposal would.
	if _, err := db.Exec(
		`INSERT INTO memories (text, kind, salience, confidence, source, created, fingerprint, quarantined, uid)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		text, string(Fact), 0.5, 0.7, "manual", time.Now().Unix(), fingerprint(text), computeUID(Fact, text, "")); err != nil {
		t.Fatal(err)
	}

	flatPath := filepath.Join(dir, Dir, string(Fact)+".md")
	if err := os.MkdirAll(filepath.Dir(flatPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flatPath, []byte("- "+text+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Import(db, nil, "", dir); err != nil {
		t.Fatal(err)
	}

	uid := computeUID(Fact, text, "")
	if _, err := os.Stat(memoryFilePath(dir, Fact, uid)); !os.IsNotExist(err) {
		t.Errorf("want no per-memory file for a line upsert held as quarantined, got stat err=%v", err)
	}
}

// A crash between reading the legacy flat file and writing its lines out as
// per-memory files must not cost the vault anything. readKindFiles only
// reads memories/<kind>.md during migration; ExportKind — the call that
// overwrites it with the new generated digest — does not run until every
// line has a per-memory file of its own. So the window between "migration
// started" and "migration finished" is survivable by construction: whatever
// killed the process, the original flat file nobody has touched yet is still
// sitting there, complete, the same bytes a person last saw.
func TestAnInterruptedMigrationLeavesTheOldVaultReadable(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := "---\ntype: memory-store\nkind: fact\ncount: 2\n---\n\n" +
		"- the first legacy fact <!-- logos id=1 conf=0.70 sal=0.50 src=manual created=2025-01-01T00:00:00Z uses=0 -->\n" +
		"- the second legacy fact <!-- logos id=2 conf=0.70 sal=0.50 src=manual created=2025-01-01T00:00:00Z uses=0 -->\n"
	path := filepath.Join(dir, Dir, "fact.md")
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	// readKindFiles is migration's read half: it writes memories/fact/<uid>.md
	// for each legacy line it finds. Calling it alone, the way a crash right
	// after it returns would leave things, proves the file this test is
	// about has had nothing done to it — the caller (importInto) never
	// reached the ExportKind call that would replace it.
	if _, _, migrated, err := readKindFiles(db, dir, Fact); err != nil {
		t.Fatal(err)
	} else if migrated != 2 {
		t.Fatalf("want 2 lines migrated, got %d", migrated)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the legacy file is gone after migration started: %v", err)
	}
	if string(after) != legacy {
		t.Errorf("the legacy flat file changed during migration; it must stay untouched until ExportKind replaces it:\nbefore:\n%s\nafter:\n%s", legacy, after)
	}

	// And the per-memory files it wrote are real, independent of whether the
	// digest that would otherwise supersede this file ever gets written.
	matches, err := filepath.Glob(filepath.Join(dir, Dir, "fact", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("want 2 per-memory files written during migration, got %d: %v", len(matches), matches)
	}
}

// The end-to-end version of the same promise invariant 1 makes everywhere
// else: migrate a pre-A2 vault, then delete the index and rebuild it from
// the vault alone, and recall must come back identical. The second Import
// has to read the per-memory files migration just wrote, not the flat file —
// readKindFiles's generatedDigestBanner is what makes that true once the
// first Import has run ExportKind — so this also proves the digest write at
// the end of migration is not itself a second place the data could drift.
func TestMigratingAVaultThenDeletingTheIndexGivesTheSameRecall(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := "---\ntype: memory-store\nkind: fact\ncount: 2\n---\n\n" +
		"- the contract manufacturer is Pegatron <!-- logos id=1 conf=0.70 sal=0.50 src=manual created=2025-01-01T00:00:00Z uses=0 -->\n" +
		"- the BOM target is $38 <!-- logos id=2 conf=0.70 sal=0.50 src=manual created=2025-01-01T00:00:00Z uses=0 -->\n"
	if err := os.WriteFile(filepath.Join(dir, Dir, "fact.md"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	first := testDB(t)
	imported, _, migrated, err := func() (int, int, int, error) {
		n, rescued, e, err := ImportEdits(first, nil, "", dir)
		return n, rescued, e.Migrated, err
	}()
	if err != nil {
		t.Fatal(err)
	}
	if imported != 2 {
		t.Fatalf("want 2 memories imported from the legacy file, got %d", imported)
	}
	if migrated != 2 {
		t.Fatalf("want 2 lines moved to their own file by this run, got %d", migrated)
	}

	before, err := All(first)
	if err != nil {
		t.Fatal(err)
	}

	// The index.db a `rm index.db && logos index` would leave behind:
	// nothing but the vault — now the per-memory files migration just wrote,
	// plus whatever digest ExportKind rendered at the end of that same run.
	second := testDB(t)
	if _, _, err := Import(second, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	after, err := All(second)
	if err != nil {
		t.Fatal(err)
	}

	toSet := func(mems []Memory) map[string]Kind {
		out := map[string]Kind{}
		for _, m := range mems {
			out[m.Text] = m.Kind
		}
		return out
	}
	wantSet, gotSet := toSet(before), toSet(after)
	if len(gotSet) != len(wantSet) {
		t.Fatalf("recall after deleting the index has %d memories, want %d", len(gotSet), len(wantSet))
	}
	for text, kind := range wantSet {
		if gotSet[text] != kind {
			t.Errorf("recall after deleting the index lost or changed %q: want kind %s, got %q", text, kind, gotSet[text])
		}
	}

	// And the second Import did not migrate anything a second time — it read
	// the per-memory files migration already wrote, not the flat file again.
	if _, _, e, err := ImportEdits(testDB(t), nil, "", dir); err != nil {
		t.Fatal(err)
	} else if e.Migrated != 0 {
		t.Errorf("a vault already migrated was migrated again on the next index: %d lines", e.Migrated)
	}
}

// Every testDB elsewhere in this file is its own :memory: database, which
// proves nothing about this guard: an in-process Go map and a table in a
// database that only this test ever opens look identical from the test's
// point of view. The guard this test exists for only matters across two
// separate `logos memory add` invocations, which share a vault directory and
// its index.db file but nothing else — not a process, not a Go map. This
// opens index.db twice, as two unrelated *sql.DB handles, to stand in for
// that: the stamp the first write leaves behind has to be visible to a
// handle that never ran that write, or the refusal in
// TestAHandEditToTheKindDigestIsNeverSilentlyOverwritten was only ever
// exercising the one case — a single long-lived process, such as the MCP
// server — where the now-removed in-process map happened to still work.
func TestAHandEditIsCaughtAcrossSeparateProcesses(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.db")

	openHandle := func() *sql.DB {
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if err := Init(db); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		SetVault(db, dir)
		t.Cleanup(func() { SetVault(db, "") })
		return db
	}

	// Process one: store a memory, which stamps the digest it writes.
	first := openHandle()
	a := Memory{Text: "the contract manufacturer is Pegatron", Kind: Fact, Source: "manual"}
	if _, err := Store(first, nil, "", &a); err != nil {
		t.Fatal(err)
	}

	// Between processes: a person hand-edits the generated digest.
	digestPath := filepath.Join(dir, Dir, "fact.md")
	handEdited := "a line a person typed directly into the generated digest\n"
	if err := os.WriteFile(digestPath, []byte(handEdited), 0o644); err != nil {
		t.Fatal(err)
	}

	// Process two: a fresh handle, fresh process, no shared memory with the
	// first — only the vault directory and index.db are in common. It must
	// still see the stamp the first process left and refuse.
	second := openHandle()
	b := Memory{Text: "the BOM target is $38", Kind: Fact, Source: "manual"}
	if _, err := Store(second, nil, "", &b); err == nil {
		t.Fatal("a second process did not catch a hand edit the first process's stamp should have revealed")
	} else if !strings.Contains(err.Error(), "fact.md") {
		t.Errorf("the refusal from a second process does not name the digest: %v", err)
	}
}
