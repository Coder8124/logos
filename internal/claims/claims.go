// Package claims holds a checkpoint's testimony up against what the host saw
// happen in the same session.
//
// A checkpoint's verified and failed fields are an agent's own word, written
// as its context runs out, and the next agent builds on verified and will not
// re-try what failed names. Both go wrong in a way prose cannot show: a
// verified line naming a test that was never run reads exactly like one that
// was, and an empty failed after an hour of red builds reads exactly like a
// session where everything worked. The activity log is the one record here
// the agent did not choose to write, so it is the thing to check against.
//
// Nothing here refuses a checkpoint. A claim the log cannot support is marked,
// and an empty failed is questioned, because a checkpoint is written when
// there is no context left to argue with.
package claims

import (
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/activity"
)

// kindQuestioned marks the log where a session's empty failed was questioned,
// so the question is put once and not on every checkpoint after it.
const kindQuestioned = "failed-questioned"

// Report is what the log says about one checkpoint's claims.
type Report struct {
	// Session is the log's id for the session checked, or "" when the log
	// holds nothing for this project and so nothing was checked.
	Session string
	// Unseen is the verified lines that name a command this session's log
	// never ran, in the order given.
	Unseen []string
	// Failures is the session's failing shell commands, oldest first.
	Failures []string
	// Questioned is whether this session's empty failed was already asked about.
	Questioned bool
}

// fresh is how recently the session checked must have logged. A checkpoint
// comes at the end of work its own host logged as it went; a log whose newest
// event for the project is older than this was written by some other session —
// yesterday's, or a host with no hooks checkpointing now — and checking this
// checkpoint against it would mark lines it had no part in.
const fresh = 30 * time.Minute

// Check reads the activity log and holds verified up against the session that
// is checkpointing on project: the one that logged last for it, within fresh.
// Two agents on one project at once can be told apart only by the host, which
// is the limit mcpserver's hostSession lives with too.
// Recording off, no log, or no recent session is a Report with no Session: a
// log that was not kept cannot say a command did not run.
func Check(vault, project string, verified []string) (Report, error) {
	if !activity.Recording(vault) {
		return Report{}, nil
	}
	events, err := activity.Read(vault, activity.Query{})
	if err != nil {
		return Report{}, err
	}
	// Read sorts newest first but keeps file order within a second, so the
	// session that wrote last is the last of the newest second, not the first.
	var r Report
	newest := int64(-1)
	for _, e := range events {
		if e.Project != project || e.Session == "" {
			continue
		}
		if newest >= 0 && e.TS < newest {
			break
		}
		newest, r.Session = e.TS, e.Session
	}
	if r.Session == "" || time.Since(time.Unix(newest, 0)) > fresh {
		return Report{}, nil
	}
	// Programs and their first arguments as this vault has ever run them, in
	// any project, so "go test" in a verified line is read as a command and
	// "auth works" is not — there is no list of programs to keep, only the log.
	known := map[string]map[string]bool{}
	var mine [][]string
	// blind is a session command line cut short before the log kept commands
	// apart from the summary: what it ran past the cut is unknown, so no line
	// can be said not to have run.
	blind := false
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		ours := e.Project == project && e.Session == r.Session
		if ours && e.Kind == kindQuestioned {
			r.Questioned = true
		}
		if e.Kind != activity.KindTool || e.Tool != "Bash" {
			continue
		}
		// Only a failing command is asked about: a Read of a missing file or
		// an Edit that missed its string fails in nearly every session and
		// rules nothing out.
		if ours && e.Failed {
			r.Failures = append(r.Failures, e.Summary)
		}
		cmds, cut := commands(e)
		for _, words := range cmds {
			if known[words[0]] == nil {
				known[words[0]] = map[string]bool{}
			}
			if len(words) > 1 {
				known[words[0]][words[1]] = true
			}
			// A run that failed does not support "passes": it is the claim
			// this check exists to catch.
			if ours && !e.Failed {
				mine = append(mine, words)
			}
		}
		if ours && cut {
			blind = true
		}
	}
	if blind {
		return r, nil
	}
	for _, line := range verified {
		if names, ok := named(line, known); ok && !ran(names, mine) {
			r.Unseen = append(r.Unseen, line)
		}
	}
	return r, nil
}

// commands is what a logged shell call ran, and whether the summary it was
// read from was cut. The hook keeps the commands apart from the cut summary;
// an event from before it did is read from the summary, minus the last
// command when the summary ends in the cut's ellipsis.
func commands(e activity.Event) ([][]string, bool) {
	if kept, ok := e.Extra["commands"].([]any); ok {
		var out [][]string
		for _, c := range kept {
			if s, ok := c.(string); ok {
				if words := strings.Fields(s); len(words) > 0 {
					out = append(out, words)
				}
			}
		}
		return out, false
	}
	line := strings.TrimPrefix(e.Summary, "Bash: ")
	cmds := activity.Commands(line)
	if !strings.HasSuffix(line, "…") {
		return cmds, false
	}
	if len(cmds) > 0 {
		cmds = cmds[:len(cmds)-1]
	}
	return cmds, true
}

// MarkQuestioned records that session's empty failed was asked about.
func MarkQuestioned(vault, project, session string) error {
	return activity.Append(vault, activity.Event{
		Kind:    kindQuestioned,
		Project: project,
		Session: session,
		Summary: "asked about an empty failed",
	})
}

// invocation is a command a verified line names: a program, and the
// alternatives for its first argument when the line gives one the log knows —
// "go build/vet" is go with build or vet.
type invocation struct {
	program string
	args    []string
}

// named reads the command a verified line leads with, or in backticks names.
// It is a command only when its program is one this project's log has run:
// that is what keeps a prose claim from being marked for naming no command.
func named(line string, known map[string]map[string]bool) ([]invocation, bool) {
	spans := backticked(line)
	if len(spans) == 0 {
		spans = []string{line}
	}
	var out []invocation
	for _, span := range spans {
		words := fields(span)
		if len(words) == 0 || known[words[0]] == nil {
			continue
		}
		inv := invocation{program: words[0]}
		if len(words) > 1 {
			for _, alt := range alternatives(words[1]) {
				if known[words[0]][alt] {
					inv.args = append(inv.args, alt)
				}
			}
		}
		out = append(out, inv)
	}
	return out, len(out) > 0
}

// ran reports whether every invocation a line names has a match in the
// session's commands. A program alone matches any run of it; with arguments,
// a run with any one of them.
func ran(names []invocation, mine [][]string) bool {
	for _, inv := range names {
		found := false
		for _, words := range mine {
			if words[0] != inv.program {
				continue
			}
			if len(inv.args) == 0 {
				found = true
			}
			for _, a := range inv.args {
				if len(words) > 1 && words[1] == a {
					found = true
				}
			}
			if found {
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// fields splits prose into words, with the punctuation prose puts after a
// command — "go test ./...:" — taken off.
func fields(s string) []string {
	var out []string
	for _, w := range strings.Fields(s) {
		if w = strings.TrimRight(w, ",;:"); w != "" {
			out = append(out, w)
		}
	}
	return out
}

func backticked(s string) []string {
	var out []string
	for {
		i := strings.IndexByte(s, '`')
		if i < 0 {
			return out
		}
		j := strings.IndexByte(s[i+1:], '`')
		if j < 0 {
			return out
		}
		out = append(out, s[i+1:i+1+j])
		s = s[i+2+j:]
	}
}

// alternatives reads "build/vet/gofmt" as three words and anything else,
// "./..." or "internal/auth", as itself; only the log's own arguments are kept
// from either, so a path is never mistaken for a list.
func alternatives(w string) []string {
	if !strings.Contains(w, "/") {
		return []string{w}
	}
	return append([]string{w}, strings.Split(w, "/")...)
}
