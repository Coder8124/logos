package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Coder8124/brain/internal/vault"
)

// A Plan is what an agent proposed before any of it was carried out — the
// half of a session a Checkpoint cannot supply, because a checkpoint is
// written after the fact and records what happened, not what was approved
// beforehand. Claude Code's plan mode already produces this text and a
// person already reviewed it; today it is discarded the moment the session
// that wrote it ends, and the next agent to resume sees only the
// checkpoint's one-sentence Next field.
type Plan struct {
	Project string
	Agent   string
	Text    string
	TS      int64
	Slug    string // vault slug, set once written
}

// PlanDir nests under a project's own checkpoint directory — sessions/<project>/plans —
// rather than beside it, so a plan travels with the continuity of the project
// that produced it and inherits the same worktree scoping checkpoints already
// have (see safeScope). Nesting also keeps it out of History's and
// latestCheckpoint's checkpoint-only scans for free: History reads its
// directory non-recursively and skips subdirectories, and latestCheckpoint's
// recursive walk filters on IsCheckpointFile, which this directory's
// filenames are built to never match — see the naming note on planFilename.
const PlanDir = "plans"

func (p Plan) Empty() bool {
	return strings.TrimSpace(p.Text) == ""
}

// SavePlan writes an approved plan to the vault and returns its slug.
//
// Called from the ExitPlanMode hook path, which runs on the critical path of
// every plan a user approves and must never block or fail loudly — so unlike
// Checkpoint.Commit this has no session to close and no index to touch; it
// only ever appends one new file. `brain index` picks it up on the next run
// the same as any other vault note.
func SavePlan(vaultDir string, p Plan) (string, error) {
	if strings.TrimSpace(p.Project) == "" {
		return "", fmt.Errorf("a plan needs a project")
	}
	if p.Empty() {
		return "", fmt.Errorf("a plan needs text")
	}
	if p.TS == 0 {
		p.TS = time.Now().Unix()
	}
	agent := p.Agent
	if agent == "" {
		agent = "agent"
	}

	dir := filepath.Join(vaultDir, CheckpointDir, filepath.FromSlash(safeScope(p.Project)), PlanDir)
	if err := vault.MkdirPrivate(dir); err != nil {
		return "", err
	}
	name := planFilename(p.TS, agent)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(p.Markdown()), vault.FileMode); err != nil {
		return "", err
	}
	slug := filepath.ToSlash(filepath.Join(CheckpointDir, safeScope(p.Project), PlanDir, strings.TrimSuffix(name, ".md")))
	return slug, nil
}

// planFilename is prefixed "plan-", not a bare timestamp, so it can never
// satisfy IsCheckpointFile's "8 digits then a dash" test. That test exists to
// keep exactly this kind of file — a second thing living in a session
// directory — from being misread as the most recent checkpoint; a plan file
// that happened to start with 8 digits would reintroduce the uncommitted.md
// bug this session already fixed once, in a new place.
func planFilename(ts int64, agent string) string {
	return fmt.Sprintf("plan-%s-%s.md", time.Unix(ts, 0).Format("20060102-150405"), safeScope(agent))
}

// Markdown renders the plan as a vault note: indexed by `brain index` like any
// other, reachable by `why` and `recall` through the plan_of relation, exactly
// the way a checkpoint reaches its project through checkpoint_of.
func (p Plan) Markdown() string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("type: plan\n")
	fmt.Fprintf(&b, "title: %s\n", yamlStr(p.title()))
	fmt.Fprintf(&b, "project: %s\n", yamlStr(p.Project))
	fmt.Fprintf(&b, "agent: %s\n", yamlStr(p.Agent))
	b.WriteString("relations:\n")
	fmt.Fprintf(&b, "  - { pred: plan_of, obj: \"[[%s]]\", conf: 1.0, src: stated }\n", p.Project)
	fmt.Fprintf(&b, "first_seen: %s\n", time.Unix(p.TS, 0).Format("2006-01-02"))
	fmt.Fprintf(&b, "planned: %s\n", time.Unix(p.TS, 0).Format(time.RFC3339))
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(p.Text))
	b.WriteString("\n")
	return b.String()
}

func (p Plan) title() string {
	t := oneLine(p.Text)
	if t == "" {
		t = "plan"
	}
	if len(t) > 70 {
		t = strings.TrimSpace(t[:70]) + "…"
	}
	return p.Project + " — " + t
}

type planFM struct {
	Project string `yaml:"project"`
	Agent   string `yaml:"agent"`
	Planned string `yaml:"planned"`
}

// ParsePlan reads a plan note back. Markdown and ParsePlan are inverses, the
// same contract Checkpoint's vault note keeps, and for the same reason: it is
// what lets a plan survive a rebuilt index.
func ParsePlan(raw string) Plan {
	fmStr, body := splitFM(raw)
	var fm planFM
	if fmStr != "" {
		_ = yaml.Unmarshal([]byte(fmStr), &fm)
	}
	p := Plan{
		Project: fm.Project,
		Agent:   fm.Agent,
		Text:    strings.TrimSpace(body),
	}
	if t, err := time.Parse(time.RFC3339, fm.Planned); err == nil {
		p.TS = t.Unix()
	}
	return p
}

// ListPlans returns a project's captured plans, newest first. This is what
// `brain plans <project>` reads, and what proves a plan the hook saved is
// actually reachable rather than write-only.
func ListPlans(vaultDir, project string) ([]Plan, error) {
	dir := filepath.Join(vaultDir, CheckpointDir, filepath.FromSlash(safeScope(project)), PlanDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	out := make([]Plan, 0, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue // one unreadable plan must not hide the ones we can read
		}
		p := ParsePlan(string(raw))
		if p.Empty() {
			continue
		}
		p.Slug = filepath.ToSlash(filepath.Join(CheckpointDir, safeScope(project), PlanDir, strings.TrimSuffix(name, ".md")))
		out = append(out, p)
	}
	return out, nil
}
