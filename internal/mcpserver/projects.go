package mcpserver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/ops"
	"github.com/Coder8124/logos/internal/project"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/untrusted"
)

// knownProjects ends a refusal for a missing project with the projects that
// have checkpoints, newest first. A model that does not know the name has no
// other way to learn it from the refusal, and a host that launches the server
// in / never supplies one.
func (s *Server) knownProjects() string {
	ps := s.checkpointedProjects()
	if len(ps) == 0 {
		return ""
	}
	const maxKnown = 5
	if len(ps) > maxKnown {
		ps = ps[:maxKnown]
	}
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = fmt.Sprintf("%s (%s)", untrusted.Inline(p.name), project.Age(p.ts))
		if p.agent != "" {
			parts[i] = fmt.Sprintf("%s (%s, %s)", untrusted.Inline(p.name), project.Age(p.ts), untrusted.Inline(p.agent))
		}
	}
	return ". Known projects: " + strings.Join(parts, ", ")
}

// projectExists reports whether the vault has ever heard this name. See
// ops.ProjectExists.
func (s *Server) projectExists(name string) bool {
	return ops.ProjectExists(s.DB, s.vault, name)
}

// knownProjectsSentence is knownProjects punctuated as an answer rather than
// as the tail of a refusal.
func (s *Server) knownProjectsSentence() string {
	if known := s.knownProjects(); known != "" {
		return known + "."
	}
	return "."
}

type knownProject struct {
	name, agent string
	ts          int64
}

// checkpointedProjects lists the projects that have a checkpoint, most recent
// first.
func (s *Server) checkpointedProjects() []knownProject {
	// Scopes, not Projects: a scope this returns is one an agent will pass
	// straight back to resume, and a worktree scope was the single thing none
	// of these surfaces could name.
	names, err := session.Scopes(s.vault)
	if err != nil {
		return nil
	}
	var ps []knownProject
	for _, n := range names {
		if h, err := session.History(s.vault, n, 1); err == nil && len(h) > 0 {
			ps = append(ps, knownProject{n, h[0].Agent, h[0].TS})
		}
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].ts > ps[j].ts })
	return ps
}

// scopeCount is a scope and how much work is filed under it.
type scopeCount struct {
	name string
	n    int
}

// checkpointedScopes lists the scopes holding at least one checkpoint, with
// the count. Separate from checkpointedProjects because that one carries the
// most recent checkpoint's agent and timestamp and this one only needs a
// number; both drop the scopes holding none, which is the part that matters.
func (s *Server) checkpointedScopes() []scopeCount {
	names, err := session.Scopes(s.vault)
	if err != nil {
		return nil
	}
	var out []scopeCount
	for _, n := range names {
		h, err := session.History(s.vault, n, 0)
		if err != nil || len(h) == 0 {
			continue
		}
		out = append(out, scopeCount{n, len(h)})
	}
	return out
}

// listProjectsHere is listProjects with the one fact it could never supply:
// where the caller is standing.
//
// listProjects is a method on Server, and its line in the tool switch was the
// only one that threaded no session state — so the single tool whose answer is
// a list of names had no way to mark the name belonging to the agent asking.
// An agent handed four names fans out and calls resume once per name; three of
// those answers are somebody else's work. The scope comes from the same
// observed sources every other continuity tool uses, never from an argument.
//
// The resource surface keeps the unscoped listing: logos://projects is a
// directory of the vault, not advice to an agent standing somewhere.
func (s *Session) listProjectsHere() (string, error) {
	body, err := s.listProjects()
	if err != nil {
		return "", err
	}
	here := s.resolveScope("")
	if here == "" {
		return body, nil
	}
	head := fmt.Sprintf("You are in %s", untrusted.Inline(here))
	if h, err := session.History(s.vault, here, 0); err == nil && len(h) > 0 {
		word := "checkpoints"
		if len(h) == 1 {
			word = "checkpoint"
		}
		head += fmt.Sprintf(" (%d %s)", len(h), word)
	} else {
		head += " (no checkpoints yet)"
	}
	return head + ".\n\nEverything in this vault:\n" + body, nil
}

// listProjects enumerates the projects logos detected, most-recently-active
// first, so a host can navigate the memory by the work it is organised around.
func (s *Server) listProjects() (string, error) {
	ps, err := project.Detect(s.DB)
	if err != nil {
		return "", err
	}
	if len(ps) == 0 {
		// The activity rollup is not where checkpoints live. A model looking
		// for a name to resume was told there were none while sessions/ held
		// them, and reported an empty memory. `logos projects` falls back the
		// same way.
		// Only scopes that actually hold a checkpoint. This branch used to print
		// every session directory under a heading asserting they all had one,
		// contradicting itself on the rows reading "(0 checkpoints)" — and those
		// empty rows are the ghost projects a host leaves behind in any folder
		// it was opened in, so the list was advertising its own exhaust.
		if ps := s.checkpointedScopes(); len(ps) > 0 {
			var b strings.Builder
			// A statement, not an instruction. "call resume with one" was the
			// only line in this server aimed at the model, and it sat directly
			// above a list — which a thorough agent reads as "enumerate these",
			// and did: four resume calls where one was wanted.
			b.WriteString("No activity rollup yet. These scopes hold checkpoints:\n")
			for _, p := range ps {
				word := "checkpoints"
				if p.n == 1 {
					word = "checkpoint"
				}
				fmt.Fprintf(&b, "- %s (%d %s)\n", p.name, p.n, word)
			}
			return strings.TrimRight(b.String(), "\n"), nil
		}
		return "No projects detected yet.", nil
	}
	var b strings.Builder
	for _, p := range ps {
		fmt.Fprintf(&b, "- %s (last active %s)\n", p.Name, project.Age(p.LastActive))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
