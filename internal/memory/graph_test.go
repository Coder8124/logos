package memory

import (
	"strings"
	"testing"
)

func TestBuildGraphLinksMemoriesToNotes(t *testing.T) {
	db := testDB(t)
	// Stand up a tiny notes/aliases schema like the index would.
	db.Exec(`CREATE TABLE notes (slug TEXT PRIMARY KEY, path TEXT, title TEXT, kind TEXT, body TEXT, hash TEXT, first_seen INTEGER DEFAULT 0)`)
	db.Exec(`CREATE TABLE aliases (slug TEXT, alias TEXT)`)
	db.Exec(`INSERT INTO notes (slug, path, title, kind, body, hash) VALUES ('people/sarah-chen','p','Sarah Chen','person','','h')`)
	db.Exec(`INSERT INTO notes (slug, path, title, kind, body, hash) VALUES ('projects/elysee','p','ÉlyséeBot','project','','h')`)
	db.Exec(`INSERT INTO aliases (slug, alias) VALUES ('projects/elysee','Elysee')`)

	storeVec(t, db, "Sarah Chen approves the ÉlyséeBot budget", Person, 0.7, []float32{1, 0, 0})
	storeVec(t, db, "Elysee ships on Friday", Context, 0.7, []float32{0, 1, 0})
	storeVec(t, db, "unrelated preference about tea", Preference, 0.5, []float32{0, 0, 1})

	g, err := BuildGraph(db, false)
	if err != nil {
		t.Fatal(err)
	}

	mentions := map[string]int{}
	for _, e := range g.Edges {
		if e.Rel == "mentions" {
			mentions[e.Dst]++
		}
	}
	if mentions["people/sarah-chen"] == 0 {
		t.Error("memory naming Sarah Chen should link to her note")
	}
	// ÉlyséeBot linked by title AND its alias 'Elysee' from a second memory.
	if mentions["projects/elysee"] < 2 {
		t.Errorf("both memories should link to the ÉlyséeBot note (title + alias), got %d", mentions["projects/elysee"])
	}
	// The tea memory names no note.
	for _, e := range g.Edges {
		if e.Rel == "mentions" && strings.Contains(nodeLabelOf(g, e.Src), "tea") {
			t.Error("the unrelated memory should not link to any note")
		}
	}
}

func TestBuildGraphSupersedesAndSimilar(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "lives in NYC", Fact, 0.5, []float32{1, 0, 0})
	storeVec(t, db, "lives in Boston", Fact, 0.5, []float32{1, 0.02, 0})
	// #1 superseded by #2.
	db.Exec("UPDATE memories SET superseded = 1, superseded_by = 2 WHERE id = 1")

	g, err := BuildGraph(db, true) // include similarity
	if err != nil {
		t.Fatal(err)
	}
	var supersedes, similar int
	for _, e := range g.Edges {
		switch e.Rel {
		case "supersedes":
			supersedes++
			if e.Src != "m2" || e.Dst != "m1" {
				t.Errorf("supersedes edge should point newer->older, got %s->%s", e.Src, e.Dst)
			}
		case "similar":
			similar++
		}
	}
	if supersedes != 1 {
		t.Errorf("want 1 supersedes edge, got %d", supersedes)
	}
	// Similar edges are only among active memories; the superseded #1 is excluded,
	// so two near-identical facts should NOT both count — only active ones pair.
	if similar != 0 {
		t.Errorf("superseded memory must not form similar edges, got %d", similar)
	}
}

func TestContainsTermWordBoundary(t *testing.T) {
	pad := func(s string) string { return " " + strings.ToLower(s) + " " }
	if !containsTerm(pad("Sarah Chen approves"), "sarah chen") {
		t.Error("should match a full name")
	}
	if containsTerm(pad("the scattered files"), "cat") {
		t.Error("must not match inside another word (scattered)")
	}
	if containsTerm(pad("go is nice"), "go") {
		t.Error("terms under 3 chars are skipped")
	}
}

func TestMermaidRenders(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "prefers short emails", Preference, 0.6, []float32{1, 0, 0})
	g, _ := BuildGraph(db, false)
	out := g.Mermaid()
	if !strings.HasPrefix(out, "graph LR") {
		t.Errorf("mermaid should start with a graph directive, got:\n%s", out)
	}
}

func nodeLabelOf(g MemGraph, id string) string {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n.Label
		}
	}
	return ""
}
