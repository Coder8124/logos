package session

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A plan saved by a Windows editor — CRLF line endings, a byte-order mark —
// is the same plan. The normalising was done by ParseCheckpoint, not by the
// splitFM both parsers share, so ParsePlan still missed the fence and read the
// plan back with no project, agent or time.
func TestAPlanSavedWithCRLFOrABOMKeepsItsFrontmatter(t *testing.T) {
	p := Plan{Project: "kestrel", Agent: "claude-code", Text: "1. fix the checkout crash", TS: 1798000000}
	md := p.Markdown()
	for name, raw := range map[string]string{
		"crlf":     strings.ReplaceAll(md, "\n", "\r\n"),
		"bom":      "\ufeff" + md,
		"bom+crlf": "\ufeff" + strings.ReplaceAll(md, "\n", "\r\n"),
	} {
		got := ParsePlan(raw)
		if got.Project != p.Project || got.Agent != p.Agent || got.TS != p.TS {
			t.Errorf("%s: read back as project %q agent %q ts %d, want %q %q %d", name, got.Project, got.Agent, got.TS, p.Project, p.Agent, p.TS)
		}
		if got.Text != p.Text {
			t.Errorf("%s: text %q, want %q", name, got.Text, p.Text)
		}
	}
}

type relationsOnly struct {
	Relations []struct {
		Pred string `yaml:"pred"`
		Obj  string `yaml:"obj"`
	} `yaml:"relations"`
}

func relationObj(t *testing.T, md, pred string) string {
	t.Helper()
	fm, _ := splitFM(md)
	var r relationsOnly
	if err := yaml.Unmarshal([]byte(fm), &r); err != nil {
		t.Fatalf("the frontmatter does not parse: %v\n%s", err, fm)
	}
	for _, rel := range r.Relations {
		if rel.Pred == pred {
			return rel.Obj
		}
	}
	t.Fatalf("no %s relation in:\n%s", pred, fm)
	return ""
}

// plan_of was written bare inside its quotes, the bug already fixed for
// checkpoint_of: a quote or backslash in the project ended the string, and
// the whole frontmatter failed to parse.
func TestAPlanForAProjectWithAQuoteOrBackslashStillParses(t *testing.T) {
	for _, project := range []string{`say "hi"`, `back\slash`, `kes]]trel`} {
		p := Plan{Project: project, Agent: "claude-code", Text: "do it", TS: 1798000000}
		md := p.Markdown()
		if got := relationObj(t, md, "plan_of"); got != "[["+project+"]]" {
			t.Errorf("plan_of for %q is %q", project, got)
		}
		if got := ParsePlan(md); got.Project != project {
			t.Errorf("project %q read back as %q", project, got.Project)
		}
	}
}

// A directory name need not be UTF-8. The project field kept it, but the
// link was JSON-encoded, which replaces every invalid byte with U+FFFD — so
// checkpoint_of named a project that does not exist and the checkpoint was
// reachable from nothing.
func TestALinkToAProjectWhoseNameIsNotUTF8NamesThatProject(t *testing.T) {
	project := "kes\xfftrel"
	c := Checkpoint{Project: project, Agent: "claude-code", Session: "20261028-101500-claude-code", TS: 1798000000}
	md := c.Markdown("20261027-090000-claude\xfe")
	if got := relationObj(t, md, "checkpoint_of"); got != "[["+project+"]]" {
		t.Errorf("checkpoint_of is %q, want %q", got, "[["+project+"]]")
	}
	if got := relationObj(t, md, "follows"); got != "[[20261027-090000-claude\xfe]]" {
		t.Errorf("follows is %q", got)
	}
	p := Plan{Project: project, Agent: "claude-code", Text: "do it", TS: 1798000000}
	if got := relationObj(t, p.Markdown(), "plan_of"); got != "[["+project+"]]" {
		t.Errorf("plan_of is %q", got)
	}
}
