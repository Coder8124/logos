package session

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Coder8124/brain/internal/gitstate"
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
	// The observed half. In frontmatter rather than a prose section because it
	// is structured, machine-written, and the thing a later query will filter on
	// — "what was the tree at, when this was decided" is a lookup, not reading.
	if !c.Git.Empty() {
		if c.Git.Branch != "" {
			fmt.Fprintf(&b, "branch: %s\n", yamlStr(c.Git.Branch))
		}
		if c.Git.Commit != "" {
			fmt.Fprintf(&b, "commit: %s\n", yamlStr(c.Git.Commit))
		}
		if c.Git.Subject != "" {
			fmt.Fprintf(&b, "commit_subject: %s\n", yamlStr(c.Git.Subject))
		}
		if c.Git.Dirty > 0 {
			fmt.Fprintf(&b, "uncommitted: %d\n", c.Git.Dirty)
		}
		if c.Git.Worktree != "" {
			fmt.Fprintf(&b, "worktree: %s\n", yamlStr(c.Git.Worktree))
		}
		// The paths git observed as changed. Collected since gitstate existed and
		// then dropped on the floor here, which quietly cost `brain why` its best
		// input: that command joins a path against what a checkpoint touched, and
		// the only list it could see was the agent's own `files` — optional over
		// MCP and not settable from the CLI at all. So the feature reported "no
		// checkpoint mentions this file" about files named in the checkpoint
		// sitting right in front of it.
		//
		// Separate from `## Files` on purpose. That section is what the agent
		// says it worked on; this is what the repository says changed. When they
		// disagree the difference is worth being able to see.
		if len(c.Git.Files) > 0 {
			b.WriteString("touched:\n")
			for _, f := range c.Git.Files {
				fmt.Fprintf(&b, "  - %s\n", yamlStr(f))
			}
		}
	}
	b.WriteString("relations:\n")
	fmt.Fprintf(&b, "  - { pred: checkpoint_of, obj: \"[[%s]]\", conf: 1.0, src: stated }\n", c.Project)
	if follows != "" {
		fmt.Fprintf(&b, "  - { pred: follows, obj: \"[[%s]]\", conf: 1.0, src: stated }\n", follows)
	}
	// first_seen is what the indexer and the graph's time scrubber read, and it
	// is day-resolution. checkpointed carries the real instant, because "how
	// long ago did the last agent stop" is a question an agent will reason about
	// and rounding it to a date turns seconds into hours.
	fmt.Fprintf(&b, "first_seen: %s\n", time.Unix(c.TS, 0).Format("2006-01-02"))
	fmt.Fprintf(&b, "checkpointed: %s\n", time.Unix(c.TS, 0).Format(time.RFC3339))
	b.WriteString("---\n")

	section(&b, secTask, c.Task)
	section(&b, secState, c.State)
	bullets(&b, secDecisions, c.Decisions)
	bullets(&b, secFailed, c.Failed)
	bullets(&b, secVerified, c.Verified)
	bullets(&b, secBlockers, c.Blockers)
	bullets(&b, secCommands, c.Commands)
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
	secVerified  = "Verified"
	secBlockers  = "Blockers"
	secCommands  = "Commands run"
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
	Project      string `yaml:"project"`
	Agent        string `yaml:"agent"`
	Session      string `yaml:"session"`
	HandoffTo    string `yaml:"handoff_to"`
	FirstSeen    string `yaml:"first_seen"`
	Checkpointed string `yaml:"checkpointed"`
	// The observed half, round-tripped so a rebuilt index and a hand-read file
	// agree with each other.
	Branch        string   `yaml:"branch"`
	Commit        string   `yaml:"commit"`
	CommitSubject string   `yaml:"commit_subject"`
	Uncommitted   int      `yaml:"uncommitted"`
	Worktree      string   `yaml:"worktree"`
	Touched       []string `yaml:"touched"`
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
		Git: gitstate.State{
			Branch:   fm.Branch,
			Commit:   fm.Commit,
			Subject:  fm.CommitSubject,
			Dirty:    fm.Uncommitted,
			Worktree: fm.Worktree,
			Files:    fm.Touched,
		},
	}
	// Prefer the precise instant; fall back to the date for checkpoints written
	// before that field existed, or hand-edited ones.
	if t, err := time.Parse(time.RFC3339, fm.Checkpointed); err == nil {
		c.TS = t.Unix()
	} else if t, err := time.Parse("2006-01-02", fm.FirstSeen); err == nil {
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
		// Absent from every checkpoint written before these existed, which is
		// exactly what the switch already handles: an unrecognised heading is
		// skipped and a missing one leaves its field nil.
		case secVerified:
			c.Verified = parseBullets(text)
		case secBlockers:
			c.Blockers = parseBullets(text)
		case secCommands:
			c.Commands = parseBullets(text)
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
