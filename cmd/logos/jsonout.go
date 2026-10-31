package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/Coder8124/logos/internal/contextpack"
	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/ops"
	"github.com/Coder8124/logos/internal/session"
)

// jsonVerbs answer --json with an outcome block, docs/logos.schema.json's
// shape. Listed so main can print a failed block for them when they error: a
// binding reading stdout would otherwise get nothing, and nothing parses the
// same as nothing found.
var jsonVerbs = []string{"recall", "context", "resume", "tried"}

// jsonFlag reports --json and returns args without it, so the flag never ends
// up inside a query or a task.
func jsonFlag(args []string) (bool, []string) {
	if !slices.Contains(args, "--json") {
		return false, args
	}
	return true, slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "--json" })
}

// printJSONFailure is a command's error as the block a --json caller parses.
// stderr still gets the error, from main, for the person watching.
func printJSONFailure(cmd string, args []string, err error) {
	if !slices.Contains(jsonVerbs, cmd) || !slices.Contains(args, "--json") {
		return
	}
	var done jsonPrinted
	if errors.As(err, &done) {
		return // the command already wrote a fuller failure block than this one
	}
	printJSON(struct {
		Outcome ops.Block `json:"outcome"`
	}{ops.Failure(err)})
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

type recallJSON struct {
	Query    string         `json:"query"`
	Memories []ops.Recalled `json:"memories"`
	Outcome  ops.Block      `json:"outcome"`
}

func printRecallJSON(query string, r ops.RecallResult, notes ...string) error {
	out := recallJSON{Query: query, Memories: r.Memories}
	if out.Memories == nil {
		out.Memories = []ops.Recalled{}
	}
	if r.Project == "" {
		out.Outcome.Searched = []ops.Searched{{AllProjects: true}}
	} else {
		out.Outcome.Searched = []ops.Searched{{Project: r.Project}}
	}
	out.Outcome.Returned = len(r.Memories)
	out.Outcome.Notes = notes
	out.Outcome.Absorb(r.Outcome)
	out.Outcome.Settle()
	return printJSON(out)
}

// packJSON is a context pack for a program. Pack is the rendered answer, the
// one the budget was spent on and the thing to hand an agent; the fields
// beside it are there for a caller that has to act on part of it — a bench
// reading the checkpoint, a tool listing the sources — without parsing
// markdown.
type packJSON struct {
	Task       string              `json:"task"`
	Project    string              `json:"project,omitempty"`
	Checkpoint *session.Checkpoint `json:"checkpoint,omitempty"`
	Sources    []string            `json:"sources"`
	Excluded   []string            `json:"excluded"`
	Budget     contextpack.Budget  `json:"budget"`
	Pack       string              `json:"pack"`
	Outcome    ops.Block           `json:"outcome"`
}

// newPackJSON renders pack, which is what fills its sources, exclusions and
// spend, and counts the block from the budget's own lines: an item a section
// kept is returned, one it could not fit is omitted for budget.
func newPackJSON(pack *contextpack.Pack, hint string, notes []string) packJSON {
	out := packJSON{Task: pack.Task, Project: hint, Checkpoint: pack.Checkpoint, Pack: pack.Render()}
	if pack.Project != nil {
		out.Project = pack.Project.Name
	}
	out.Sources, out.Excluded, out.Budget = pack.Sources, pack.Excluded, pack.Budget
	if out.Sources == nil {
		out.Sources = []string{}
	}
	if out.Excluded == nil {
		out.Excluded = []string{}
	}
	if out.Project != "" {
		out.Outcome.Searched = []ops.Searched{{Project: out.Project}}
	}
	for _, l := range pack.Budget.By {
		out.Outcome.Returned += l.Items
		out.Outcome.Omitted.Budget += l.Dropped
	}
	// A window the filter abandoned for matching nothing dated was not
	// applied, and reporting it would claim a filter that did not run.
	if pack.Window != nil && !pack.WindowEmpty {
		out.Outcome.Window = ops.NewWindow(pack.Window.From, pack.Window.To, pack.OutOfWindow)
	}
	out.Outcome.Notes = notes
	out.Outcome.Settle()
	return out
}

type sessionChainJSON struct {
	HostSession string               `json:"host_session"`
	Checkpoints []session.Checkpoint `json:"checkpoints"`
	Outcome     ops.Block            `json:"outcome"`
}

func printSessionChainJSON(hostSession string, chain []session.Checkpoint) error {
	out := sessionChainJSON{HostSession: hostSession, Checkpoints: chain}
	out.Outcome.Returned = len(chain)
	out.Outcome.Settle()
	return printJSON(out)
}

type conversationJSON struct {
	Project string `json:"project"`
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	// Newest first, the way resume reads them: the first is where it stopped.
	Checkpoints []session.Checkpoint `json:"checkpoints"`
	Outcome     ops.Block            `json:"outcome"`
}

// jsonPrinted is a failure whose --json block the command printed itself,
// because it carries more than the error: a second, bare block after it
// would be two documents on stdout and unparseable.
type jsonPrinted struct{ error }

func (e jsonPrinted) Unwrap() error { return e.error }

type conversationCandidateJSON struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Latest string `json:"latest_checkpoint"`
}

// printConversationMissJSON is a conversation name that resolved to none or
// to several. The candidates are the point: the error tells a script to name
// one by id, and it can only do that if it is given the ids.
func printConversationMissJSON(project, query string, candidates []session.Conversation, notes []string, err error) error {
	out := struct {
		Project    string                      `json:"project"`
		Query      string                      `json:"conversation"`
		Candidates []conversationCandidateJSON `json:"candidates"`
		Outcome    ops.Block                   `json:"outcome"`
	}{Project: project, Query: query, Candidates: []conversationCandidateJSON{}, Outcome: ops.Failure(err)}
	for _, c := range candidates {
		out.Candidates = append(out.Candidates, conversationCandidateJSON{ID: c.ID, Name: c.Name, Latest: c.Latest.Slug})
	}
	out.Outcome.Notes = append(out.Outcome.Notes, notes...)
	if perr := printJSON(out); perr != nil {
		return perr
	}
	return jsonPrinted{err}
}

// printConversationJSON is resume narrowed to one conversation, for a script.
// notes is what resume already said on the way (a sweep that recorded an
// unsaved session, say): narrowing the answer must not make that silent.
func printConversationJSON(project string, c session.Conversation, notes []string) error {
	out := conversationJSON{Project: project, ID: c.ID, Name: c.Name, Checkpoints: c.Chain}
	out.Outcome.Returned = len(c.Chain)
	out.Outcome.Notes = append(out.Outcome.Notes, notes...)
	if c.Flat {
		out.Outcome.Notes = append(out.Outcome.Notes, "some of this conversation is still in the old flat layout — logos migrate sessions")
	}
	out.Outcome.Settle()
	return printJSON(out)
}

type triedJSON struct {
	Proposed string           `json:"proposed"`
	Rulings  []deadend.Ruling `json:"rulings"`
	Outcome  ops.Block        `json:"outcome"`
}

// printTriedJSON reports a dead-end check. The corpus is every project's, the
// asked-about one only deciding which hits are marked as from elsewhere, so
// that is what searched says. A semantic arm that broke partway is an error —
// the check was weaker than the caller will assume — but one that was never
// on is how a machine with no model is configured, and is said as a note.
func printTriedJSON(proposed string, hits []deadend.Ruling, semanticErr error) error {
	out := triedJSON{Proposed: proposed, Rulings: hits}
	if out.Rulings == nil {
		out.Rulings = []deadend.Ruling{}
	}
	out.Outcome.Searched = []ops.Searched{{AllProjects: true}}
	out.Outcome.Returned = len(hits)
	if semanticErr != nil {
		msg := strings.Trim(strings.TrimSpace(deadend.SemanticSkipped(semanticErr)), "_")
		if errors.Is(semanticErr, deadend.ErrNoLocalModel) {
			out.Outcome.Notes = append(out.Outcome.Notes, msg)
		} else {
			out.Outcome.Errors = append(out.Outcome.Errors, fmt.Sprintf("semantic check: %v", semanticErr))
		}
	}
	out.Outcome.Settle()
	return printJSON(out)
}
