package memory

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The claim under test is the project's second principle, applied to the half
// of the system where it used to be false: delete .brain, reindex, get the same
// state back.

func vaultDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db := testDB(t)
	dir := t.TempDir()
	SetVault(dir)
	t.Cleanup(func() { SetVault("") })
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

	// The wipe. A fresh database, as if .brain had been deleted.
	wiped := testDB(t)
	n, err := Import(wiped, nil, "", dir)
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
	if _, err := Import(wiped, nil, "", dir); err != nil {
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
// otherwise "markdown is truth" is decoration.
func TestEditingTheFileCorrectsTheMemory(t *testing.T) {
	db, dir := vaultDB(t)

	m := Memory{Text: "The launch price is $199", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, Dir, "fact.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), "$199", "$249", 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Import(db, nil, "", dir); err != nil {
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
// or it is an append-only log wearing a document's clothes.
func TestDeletingALineForgetsIt(t *testing.T) {
	db, dir := vaultDB(t)

	keep := Memory{Text: "Tomas runs manufacturing", Kind: Person, Source: "manual"}
	drop := Memory{Text: "Sarah is the CFO", Kind: Person, Source: "manual"}
	for _, m := range []*Memory{&keep, &drop} {
		if _, err := Store(db, nil, "", m); err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(dir, Dir, "person.md")
	raw, _ := os.ReadFile(path)
	var kept []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, "Sarah is the CFO") {
			kept = append(kept, line)
		}
	}
	os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644)

	if _, err := Import(db, nil, "", dir); err != nil {
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

	n, err := Import(db, nil, "", dir)
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

	if _, err := Import(db, nil, "", dir); err != nil {
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
		"<!-- brain id=",
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
