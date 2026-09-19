package memory

import (
	"os"
	"strings"
	"testing"
)

// The kind file wrote project=billing api unescaped, the parser stopped at the
// space, and the reconcile that runs before every write read "billing" back
// into the index — so storing any other memory moved this one into a different
// project, silently, in the vault and the cache alike.
func TestAMemoryInAProjectWhoseNameHasASpaceStaysInThatProject(t *testing.T) {
	db, dir := store(t)
	if _, err := Store(db, nil, "", &Memory{Text: "billing uses stripe", Kind: Fact, Source: "test", Project: "billing api"}); err != nil {
		t.Fatal(err)
	}
	// A new process has no record of having written the file, so its first
	// write reconciles the file back into the index — which is every CLI call.
	dropStamps(db)
	if _, err := Store(db, nil, "", &Memory{Text: "an unrelated fact", Kind: Fact, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	var project string
	if err := db.QueryRow(`SELECT project FROM memories WHERE text = 'billing uses stripe'`).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if project != "billing api" {
		t.Errorf("project = %q after an unrelated write, want %q", project, "billing api")
	}
	raw, err := os.ReadFile(path(dir, Fact))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); strings.Contains(got, "project=billing -->") {
		t.Errorf("the vault files the memory under a project that does not exist:\n%s", got)
	}
}

// Lines written before the field was escaped carry a bare name, and must keep
// reading as that name.
func TestAnUnescapedProjectFieldStillReadsAsWritten(t *testing.T) {
	ms := parseKind(Fact, "- the drop test is 1.2m <!-- logos id=3 conf=0.90 sal=0.70 src=test created=2026-01-01T00:00:00Z uses=0 project=kestrel -->\n")
	if len(ms) != 1 || ms[0].Project != "kestrel" {
		t.Fatalf("parsed %+v, want one memory in project kestrel", ms)
	}
}
