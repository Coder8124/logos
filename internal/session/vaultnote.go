package session

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The checkpoint's on-disk form.
//
// It is a plain vault note — the same frontmatter dialect internal/vault
// already parses — which is the whole reason for choosing markdown over a
// table. Because it is a note, `brain index` embeds it, the graph links it to
// its project through the checkpoint_of relation, and "where did we leave off?"
// becomes an ordinary retrieval question that needed no retrieval code. A row
// in SQLite would have bought none of that.
//
// Markdown and ParseCheckpoint are inverses, and the round trip is tested. That
// property is what lets Latest read the vault directly and lets a checkpoint
// survive a rebuilt index.

// Markdown renders the checkpoint as a vault note. follows is the session id of
// the previous checkpoint, linking the chain backwards; empty for the first.
func (c Checkpoint) Markdown(follows string) string {
	var b strings.Builder

	b.WriteString("---\n")
	b.WriteString("type: checkpoint\n")
	fmt.Fprintf(&b, "title: %s\n", yamlStr(c.title()))
	fmt.Fprintf(&b, "project: %s\n", yamlStr(c.Project))
	fmt.Fprintf(&b, "agent: %s\n", yamlStr(c.Agent))
	fmt.Fprintf(&b, "session: %s\n", yamlStr(c.Session))
	if c.HandoffTo != "" {
		fmt.Fprintf(&b, "handoff_to: %s\n", yamlStr(c.HandoffTo))
	}
	b.WriteString("relations:\n")
	fmt.Fprintf(&b, "  - { pred: checkpoint_of, obj: \"[[%s]]\", conf: 1.0, src: stated }\n", c.Project)
	if follows != "" {
		fmt.Fprintf(&b, "  - { pred: follows, obj: \"[[%s]]\", conf: 1.0, src: stated }\n", follows)
	}
	fmt.Fprintf(&b, "first_seen: %s\n", time.Unix(c.TS, 0).Format("2006-01-02"))
	b.WriteString("---\n")

	section(&b, secTask, c.Task)
	section(&b, secState, c.State)
	bullets(&b, secDecisions, c.Decisions)
	bullets(&b, secFailed, c.Failed)
	bullets(&b, secQuestions, c.Questions)
	bullets(&b, secFiles, c.Files)
	section(&b, secNext, c.Next)

	return b.String()
}

// Section headings. Parsing keys off these exact strings, so they are constants
// rather than literals scattered across the renderer.
const (
	secTask      = "Task"
	secState     = "State"
	secDecisions = "Decisions"
	secFailed    = "Didn't work"
	secQuestions = "Open questions"
	secFiles     = "Files"
	secNext      = "Next"
)

func (c Checkpoint) title() string {
	t := oneLine(c.Task)
	if t == "" {
		t = oneLine(c.Next)
	}
	if t == "" {
		t = "checkpoint"
	}
	if len(t) > 70 {
		t = strings.TrimSpace(t[:70]) + "…"
	}
	return c.Project + " — " + t
}

func section(b *strings.Builder, heading, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	fmt.Fprintf(b, "\n## %s\n\n%s\n", heading, body)
}

func bullets(b *strings.Builder, heading string, items []string) {
	items = nonEmpty(items)
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## %s\n\n", heading)
	for _, it := range items {
		fmt.Fprintf(b, "- %s\n", strings.TrimSpace(it))
	}
}

type checkpointFM struct {
	Project   string `yaml:"project"`
	Agent     string `yaml:"agent"`
	Session   string `yaml:"session"`
	HandoffTo string `yaml:"handoff_to"`
	FirstSeen string `yaml:"first_seen"`
}

// ParseCheckpoint reads a checkpoint back from its note. It is forgiving in the
// same way internal/vault is: a note that has been hand-edited into a shape we
// did not write should still yield whatever is recognisable, because the
// alternative is silently losing the record.
func ParseCheckpoint(raw string) Checkpoint {
	fmStr, body := splitFM(raw)

	var fm checkpointFM
	if fmStr != "" {
		_ = yaml.Unmarshal([]byte(fmStr), &fm)
	}
	c := Checkpoint{
		Project:   fm.Project,
		Agent:     fm.Agent,
		Session:   fm.Session,
		HandoffTo: fm.HandoffTo,
	}
	if t, err := time.Parse("2006-01-02", fm.FirstSeen); err == nil {
		c.TS = t.Unix()
	}

	for heading, text := range sections(body) {
		switch heading {
		case secTask:
			c.Task = text
		case secState:
			c.State = text
		case secNext:
			c.Next = text
		case secDecisions:
			c.Decisions = parseBullets(text)
		case secFailed:
			c.Failed = parseBullets(text)
		case secQuestions:
			c.Questions = parseBullets(text)
		case secFiles:
			c.Files = parseBullets(text)
		}
	}
	return c
}

// sections splits a body into "## Heading" → text. Content before the first
// heading is discarded: it is not a section, and guessing which one it belongs
// to would be worse than dropping it.
func sections(body string) map[string]string {
	out := map[string]string{}
	var heading string
	var buf []string

	flush := func() {
		if heading != "" {
			out[heading] = strings.TrimSpace(strings.Join(buf, "\n"))
		}
		buf = buf[:0]
	}
	for _, line := range strings.Split(body, "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			flush()
			heading = strings.TrimSpace(h)
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return out
}

func parseBullets(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		for _, marker := range []string{"- ", "* ", "+ "} {
			if rest, ok := strings.CutPrefix(line, marker); ok {
				out = append(out, strings.TrimSpace(rest))
				break
			}
		}
	}
	return out
}

// splitFM peels a leading --- fenced block. Mirrors internal/vault's splitter;
// kept local because the checkpoint has its own frontmatter shape.
func splitFM(raw string) (string, string) {
	if !strings.HasPrefix(raw, "---\n") {
		return "", raw
	}
	rest := raw[4:]
	i := strings.Index(rest, "\n---\n")
	if i < 0 {
		return "", raw
	}
	return rest[:i], strings.TrimLeft(rest[i+5:], "\n")
}

// yamlStr quotes a scalar so a colon or a leading bracket in a task description
// cannot turn the frontmatter into something else.
func yamlStr(s string) string {
	b, err := yaml.Marshal(s)
	if err != nil {
		return `""`
	}
	return strings.TrimRight(string(b), "\n")
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}
