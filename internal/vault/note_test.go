package vault

import "testing"

func TestParsesFrontmatterRelationsAndBodyLinks(t *testing.T) {
	raw := "---\ntype: person\naliases: [Sam]\nrelations:\n  - { pred: works_on, obj: \"[[logos]]\", conf: 0.8, src: inferred }\n---\nWorks with [[Ana Diaz]] on [[logos]].\n"
	n := Parse("/v", "/v/people/sameer.md", raw)

	if n.Slug != "people/sameer" {
		t.Errorf("slug = %q, want people/sameer", n.Slug)
	}
	if n.Kind != "person" {
		t.Errorf("kind = %q, want person", n.Kind)
	}
	if len(n.Aliases) != 1 || n.Aliases[0] != "Sam" {
		t.Errorf("aliases = %v", n.Aliases)
	}

	// [[logos]] already exists as a typed relation, so the body mention must
	// not create a duplicate weaker edge.
	if len(n.Edges) != 2 {
		t.Fatalf("edges = %d, want 2: %+v", len(n.Edges), n.Edges)
	}

	byObj := map[string]Edge{}
	for _, e := range n.Edges {
		byObj[e.Obj] = e
	}
	if got := byObj["logos"]; got.Pred != "works_on" || got.Src != Inferred || got.Conf != 0.8 {
		t.Errorf("logos edge = %+v", got)
	}
	if got := byObj["ana-diaz"]; got.Pred != "mentions" || got.Conf != 1.0 {
		t.Errorf("ana edge = %+v", got)
	}
}

func TestMalformedFrontmatterStillIndexesBody(t *testing.T) {
	n := Parse("/v", "/v/a.md", "---\nthis: is: not: yaml\n---\nhello [[world]]\n")
	if n.Kind != "note" {
		t.Errorf("kind = %q, want note", n.Kind)
	}
	if len(n.Edges) != 1 {
		t.Errorf("edges = %d, want 1", len(n.Edges))
	}
}

func TestNoteWithoutFrontmatter(t *testing.T) {
	n := Parse("/v", "/v/a.md", "just text")
	if n.Body != "just text" {
		t.Errorf("body = %q", n.Body)
	}
	if len(n.Edges) != 0 {
		t.Errorf("edges = %d, want 0", len(n.Edges))
	}
}

func TestStripsWikilinkAliasesAndHeadings(t *testing.T) {
	n := Parse("/v", "/v/a.md", "[[logos|the app]] and [[logos#setup]]")
	if len(n.Edges) != 1 || n.Edges[0].Obj != "logos" {
		t.Errorf("edges = %+v, want one edge to logos", n.Edges)
	}
}

func TestConfidenceIsClamped(t *testing.T) {
	raw := "---\nrelations:\n  - { pred: p, obj: x, conf: 5.0 }\n  - { pred: p, obj: y, conf: -2.0 }\n---\n"
	n := Parse("/v", "/v/a.md", raw)
	for _, e := range n.Edges {
		if e.Conf < 0 || e.Conf > 1 {
			t.Errorf("conf %v out of range for %+v", e.Conf, e)
		}
	}
}

func TestSlugNormalisation(t *testing.T) {
	if got := Slug("/v", "/v/people/Sameer Rao.md"); got != "people/sameer-rao" {
		t.Errorf("Slug = %q", got)
	}
	if got := NormalizeLink("  People/Sameer Rao "); got != "sameer-rao" {
		t.Errorf("NormalizeLink = %q", got)
	}
}
