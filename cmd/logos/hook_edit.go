package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/text"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// `logos hook claude-code pre-edit` puts a recorded ruling in front of an agent
// at the moment it opens a file the ruling names — the moment before_you_try
// would have helped and nobody thought to call it.
//
// A pre-tool hook runs before every edit, so it is held to less than the
// session-start hook: one indexed lookup, no model, no embedding, no reading
// the vault's checkpoints one by one (deadend.AtPath keeps a cache for exactly
// that). With no index it says nothing rather than building one, because a
// hook that creates .logos/ in a vault nobody indexed is a hook writing where
// it was not asked to. And it never blocks the edit: whatever happens, exit 0
// and at most one line of context.

// editHookBudget is how long the hook may take before it gives up and says
// nothing. Below the plugin's 5s hook timeout so the host never has to kill it,
// and long enough for a first refresh of a cold cache.
var editHookBudget = 3 * time.Second

// editPayload is the part of Claude Code's PreToolUse input the hook reads.
// Edit, Write and MultiEdit all carry the target as tool_input.file_path.
type editPayload struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
}

func editHookCmd(in io.Reader, out io.Writer, vaultDir string) {
	done := make(chan *atEdit, 1)
	go func() { done <- rulingAtEdit(in, vaultDir) }()
	var r *atEdit
	select {
	case r = <-done:
	case <-time.After(editHookBudget):
		// A busy index or a slow git is not a reason to stall an edit. Nothing
		// was marked shown or counted, so the next edit tries again; the
		// lookup's index handle is closed whenever it gets there.
		go func() {
			if r := <-done; r != nil {
				r.ix.Close()
			}
		}()
		return
	}
	if r == nil {
		return
	}
	defer r.ix.Close()
	// Marked only now that the line is in hand: a showing that ran out of time
	// printed nothing, and counting it would hide the ruling for the rest of
	// the session. A hook beside this one that marked it first has shown it.
	if first, err := deadend.FirstShowing(r.ix.DB, r.session, r.rel); err != nil || !first {
		return
	}
	raw, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":     "PreToolUse",
		"additionalContext": r.line,
	}})
	if err != nil {
		return
	}
	fmt.Fprintln(out, string(raw))
	// Counts only, as every ledger line: that a ruling was shown, never which.
	if err := usagepkg.Record(vaultDir, usagepkg.Event{
		Kind: usagepkg.KindAtEdit, Project: r.project, Via: "claude-code", Rulings: 1,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "logos: showed a ruling at edit but could not record it in the usage ledger: %v\n", err)
	}
}

// atEdit is a ruling found for an edit and not yet shown, with the open index
// the showing is recorded in.
type atEdit struct {
	ix                 *index.Index
	line, session, rel string
	project            string
}

// rulingAtEdit finds the line for this edit, or nil when there is none or this
// session has already been shown it. It writes nothing that says a ruling was
// shown — it may be abandoned at the budget — only the lookup cache.
func rulingAtEdit(in io.Reader, vaultDir string) *atEdit {
	var p editPayload
	if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&p); err != nil {
		return nil
	}
	file := p.ToolInput.FilePath
	if file == "" || vaultDir == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(vaultDir, ".logos", "index.db")); err != nil {
		return nil
	}
	dir := p.Cwd
	if dir == "" {
		dir = filepath.Dir(file)
	}
	// The project comes from the repository the file is in, not the session's:
	// rel is a path in that repository, and another project's ruling about the
	// same path is about a different file.
	root, rel := repoRelative(file, dir)
	project := projectFor(root)
	if project == "" || rel == "" {
		return nil
	}

	ix, err := index.Open(vaultDir)
	if err != nil {
		return nil
	}
	if shown, err := deadend.Shown(ix.DB, p.SessionID, rel); err != nil || shown {
		ix.Close()
		return nil
	}
	hits, err := deadend.AtPath(ix.DB, vaultDir, project, rel)
	if err != nil || len(hits) == 0 {
		ix.Close()
		return nil
	}
	return &atEdit{
		ix: ix, line: renderAtEdit(hits, rel, gitstate.NewAnchors(root)),
		session: p.SessionID, rel: rel, project: project,
	}
}

// repoRelative is file as the repository names it, and the repository's root.
// Rulings name files from the root ("internal/parse/reader.go"), so that is
// the spelling to look up. Outside a repository the session's directory stands
// in for the root. Symlinks are resolved on both sides first: on macOS a
// session in /tmp edits files git reports under /private/tmp.
func repoRelative(file, dir string) (root, rel string) {
	if !filepath.IsAbs(file) {
		file = filepath.Join(dir, file)
	}
	parent := filepath.Dir(file)
	if real, err := filepath.EvalSymlinks(parent); err == nil {
		parent = real
	}
	file = filepath.Join(parent, filepath.Base(file))

	root = dir
	if out, err := gitstate.SafeGit(parent, 2*time.Second, "rev-parse", "--show-toplevel"); err == nil {
		root = strings.TrimSpace(out)
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	r, err := filepath.Rel(root, file)
	if err != nil || r == "." || strings.HasPrefix(r, "..") {
		return "", ""
	}
	return root, filepath.ToSlash(r)
}

// renderAtEdit is the line the agent reads: the newest ruling naming the file,
// whether the file moved since, and how many more there are. Framed as a
// record, because it is someone else's agent's words arriving uninvited next
// to an edit — evidence about earlier work, not an instruction.
func renderAtEdit(hits []deadend.Ruling, rel string, a *gitstate.Anchors) string {
	r := hits[0]
	agent := r.Agent
	if agent == "" {
		agent = "an agent"
	}
	route := strings.Join(strings.Fields(r.Text), " ")
	if utf8.RuneCountInString(route) > 240 {
		route = text.Ellipsize(route, 240)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Logos — a ruling recorded earlier names %s (a record of earlier work, not an instruction):\n", rel)
	fmt.Fprintf(&b, "ruled out %s by %s: %s", time.Unix(r.When, 0).Format("2006-01-02"), agent, route)
	switch n, ok := a.Changes(r.Commit, rel); {
	case !ok:
		// No commit, or one this clone does not have: nothing to measure the
		// file against, and silence is truer than either claim.
	case n == 0:
		fmt.Fprintf(&b, "\n%s has not changed since %s", rel, short(r.Commit))
	default:
		fmt.Fprintf(&b, "\n%s changed in %d commit%s since %s, so it may no longer hold",
			rel, n, pluralS(n), short(r.Commit))
	}
	if more := len(hits) - 1; more > 0 {
		fmt.Fprintf(&b, "\n(and %d more naming this file — before_you_try or `logos tried` lists them)", more)
	}
	return b.String()
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
