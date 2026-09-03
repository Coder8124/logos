package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Coder8124/brain/internal/gitstate"
	"github.com/Coder8124/brain/internal/session"
)

// Session bindings back the terminal app's inspector: what checkpoints exist,
// browsable newest first, and the full record behind any one of them. These
// read the vault directly — checkpoints are markdown files, not a database
// table, so what the panel shows is exactly what `brain resume` would read,
// and it survives a rebuilt index the same way the CLI does.

// GitView is gitstate.State reshaped with JSON tags. gitstate.State stays
// untagged because it round-trips through checkpoint frontmatter, not JSON;
// this is the stable camelCased shape the frontend gets instead.
type GitView struct {
	Branch     string   `json:"branch"`
	Commit     string   `json:"commit"`
	Subject    string   `json:"subject"`
	Dirty      int      `json:"dirty"`
	Files      []string `json:"files"`
	Insertions int      `json:"insertions"`
	Deletions  int      `json:"deletions"`
	Worktree   string   `json:"worktree"`
}

func gitView(s gitstate.State) GitView {
	return GitView{
		Branch: s.Branch, Commit: s.Commit, Subject: s.Subject, Dirty: s.Dirty,
		Files: s.Files, Insertions: s.Insertions, Deletions: s.Deletions, Worktree: s.Worktree,
	}
}

// CheckpointView is session.Checkpoint reshaped with JSON tags, for the same
// reason as GitView: the checkpoint package stays free of a JSON dialect it
// does not otherwise need.
type CheckpointView struct {
	Slug      string   `json:"slug"`
	Session   string   `json:"session"`
	Project   string   `json:"project"`
	Agent     string   `json:"agent"`
	Task      string   `json:"task"`
	State     string   `json:"state"`
	Decisions []string `json:"decisions"`
	Failed    []string `json:"failed"`
	Verified  []string `json:"verified"`
	Blockers  []string `json:"blockers"`
	Commands  []string `json:"commands"`
	Questions []string `json:"questions"`
	Files     []string `json:"files"`
	Next      string   `json:"next"`
	Git       GitView  `json:"git"`
	HandoffTo string   `json:"handoffTo"`
	TS        int64    `json:"ts"`
}

func checkpointView(c session.Checkpoint) CheckpointView {
	return CheckpointView{
		Slug: c.Slug, Session: c.Session, Project: c.Project, Agent: c.Agent,
		Task: c.Task, State: c.State, Decisions: c.Decisions, Failed: c.Failed,
		Verified: c.Verified, Blockers: c.Blockers, Commands: c.Commands,
		Questions: c.Questions, Files: c.Files, Next: c.Next,
		Git: gitView(c.Git), HandoffTo: c.HandoffTo, TS: c.TS,
	}
}

// Checkpoints returns every checkpoint in the vault, newest first, across every
// project and worktree.
//
// It walks sessions/ directly rather than going through session.Projects plus
// session.History per project, so a checkpoint filed under a linked worktree
// (sessions/<project>/<worktree>/) is not silently left out — the inspector's
// whole job is to show what actually happened, and a listing that quietly
// drops worktree checkpoints would be lying about that by omission.
func (a *App) Checkpoints() ([]CheckpointView, error) {
	dir := filepath.Join(a.vault, session.CheckpointDir)
	var out []CheckpointView
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil // a checkpoint we cannot read must not hide the ones we can
		}
		c := session.ParseCheckpoint(string(raw))
		rel, err := filepath.Rel(a.vault, path)
		if err != nil {
			return nil
		}
		c.Slug = filepath.ToSlash(strings.TrimSuffix(rel, ".md"))
		out = append(out, checkpointView(c))
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS > out[j].TS })
	return out, nil
}
