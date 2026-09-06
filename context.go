package brain

import (
	"fmt"
	"strings"

	"github.com/Coder8124/brain/internal/contextpack"
)

// A Request describes what you are about to do. Task is the important field:
// "continue the MCP implementation" retrieves differently from a project name
// alone, because it says which corner of the project matters right now.
type Request struct {
	// Task is what you are about to do, in a sentence. Required.
	Task string
	// Project narrows to one piece of work. Optional — when empty, the task
	// text is matched against known projects.
	Project string
	// Budget is the approximate token ceiling. Zero means 4000.
	Budget int
}

// Context assembles everything bearing on a task: where the last agent stopped,
// what it ruled out, uncommitted progress since, the project dossier, the prose
// of relevant notes, notes reached one hop through the user's own links,
// memories with their provenance, and open commitments — spent against a token
// budget and cited by source.
//
// This is the call to make at the start of a task, in preference to Recall.
// Recall answers "what do you know about X"; this answers "give me what I need
// to do X", which is not a longer version of the same question.
func (b *Brain) Context(req Request) (*Context, error) {
	pack, err := contextpack.Build(b.ix, b.embed, b.embedModel, contextpack.Request{
		Task: req.Task, Hint: req.Project, Budget: req.Budget,
	})
	if err != nil {
		return nil, err
	}
	return &pack, nil
}

// Resume picks up a project where the last agent left off. Equivalent to
// Context with a continuation task, and named separately because that is how
// people think about it.
func (b *Brain) Resume(project string) (*Context, error) {
	if strings.TrimSpace(project) == "" {
		return nil, fmt.Errorf("resume needs a project")
	}
	return b.Context(Request{Task: "resume work on " + project, Project: project})
}
