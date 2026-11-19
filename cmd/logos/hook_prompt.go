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

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/scope"
	"github.com/Coder8124/logos/internal/text"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// `logos hook claude-code prompt` puts a recorded ruling in front of an agent
// when the person typing restates an approach somebody already ruled out —
// before the agent plans around it, which is earlier than before_you_try or
// the edit hook can reach.
//
// It runs on every prompt, so it keeps the edit hook's rules: no model, no
// index built where none exists, never blocks the prompt, gives up silently at
// the budget, and says nothing when there is nothing to say.

// promptHookBudget is the edit hook's budget, for the same reason: below the
// plugin's 5s hook timeout, so the host never has to kill it.
var promptHookBudget = 3 * time.Second

// promptRulingLimit is how many rulings one prompt may be shown. A prompt that
// restates three ruled-out approaches is better served by before_you_try than
// by a wall of context the agent did not ask for.
const promptRulingLimit = 2

// promptPayload is the part of Claude Code's UserPromptSubmit input the hook
// reads.
type promptPayload struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Prompt    string `json:"prompt"`
}

func promptHookCmd(in io.Reader, out io.Writer, vaultDir string) {
	if !auto.On(vaultDir, auto.RulingsAtPrompt) {
		return
	}
	done := make(chan *atPrompt, 1)
	go func() { done <- rulingsAtPrompt(in, vaultDir) }()
	var r *atPrompt
	select {
	case r = <-done:
	case <-time.After(promptHookBudget):
		// Nothing was marked shown, so a later prompt can still show it.
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
	// Marked only now the lines are in hand, as at edit: a showing that ran
	// out of time printed nothing and must not hide the ruling for the rest of
	// the session.
	var shown []deadend.Ruling
	for _, h := range r.hits {
		if first, err := deadend.FirstShowing(r.ix.DB, r.session, promptShownKey(h)); err == nil && first {
			shown = append(shown, h)
		}
	}
	if len(shown) == 0 {
		return
	}
	raw, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":     "UserPromptSubmit",
		"additionalContext": renderAtPrompt(shown),
	}})
	if err != nil {
		return
	}
	fmt.Fprintln(out, string(raw))
	// Counts and fingerprints only, as every ledger line: never the prompt.
	if err := usagepkg.Record(vaultDir, usagepkg.Event{
		Kind: usagepkg.KindAtPrompt, Project: r.project, Via: "claude-code",
		Rulings: len(shown), Refs: usagepkg.RulingRefs(shown),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "logos: showed a ruling at the prompt but could not record it in the usage ledger: %v\n", err)
	}
}

// atPrompt is what a prompt matched and has not yet been shown, with the open
// index the showing is recorded in.
type atPrompt struct {
	ix               *index.Index
	hits             []deadend.Ruling
	session, project string
}

// promptShownKey is the cursor key for one ruling shown at the prompt. The
// edit hook keys by file; a prompt names no file, so the ruling itself is the
// key, prefixed so the two hooks never mark each other's showings.
func promptShownKey(r deadend.Ruling) string {
	return "prompt:" + usagepkg.Ref(r.Text)
}

// rulingsAtPrompt finds the rulings this prompt restates and this session has
// not been shown, or nil. It writes nothing that says a ruling was shown.
func rulingsAtPrompt(in io.Reader, vaultDir string) *atPrompt {
	var p promptPayload
	if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&p); err != nil {
		return nil
	}
	if strings.TrimSpace(p.Prompt) == "" || p.Cwd == "" || vaultDir == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(vaultDir, ".logos", "index.db")); err != nil {
		return nil
	}
	project := projectFor(promptRoot(p.Cwd))
	if project == "" {
		return nil
	}
	ix, err := index.Open(vaultDir)
	if err != nil {
		return nil
	}
	hits, err := deadend.AtPrompt(vaultDir, ix.DB, project, p.Prompt, 0)
	if err != nil {
		ix.Close()
		return nil
	}
	var fresh []deadend.Ruling
	for _, h := range hits {
		if shown, err := deadend.Shown(ix.DB, p.SessionID, promptShownKey(h)); err == nil && !shown {
			fresh = append(fresh, h)
			if len(fresh) == promptRulingLimit {
				break
			}
		}
	}
	if len(fresh) == 0 {
		ix.Close()
		return nil
	}
	return &atPrompt{ix: ix, hits: fresh, session: p.SessionID, project: project}
}

// promptRoot is the repository the prompt was typed in, spelled as the disk
// spells it, so the project matches the one its checkpoints were filed under.
// Outside a repository the directory itself stands in.
func promptRoot(cwd string) string {
	root := cwd
	if out, err := gitstate.SafeGit(cwd, 2*time.Second, "rev-parse", "--show-toplevel"); err == nil {
		root = strings.TrimSpace(out)
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	return scope.OnDisk(root)
}

// renderAtPrompt is the context the agent reads. Framed as a record, because
// it is another agent's words arriving uninvited beside the user's — evidence
// that an approach was tried, not an instruction to refuse it. The user may
// know something the ruling does not.
func renderAtPrompt(hits []deadend.Ruling) string {
	var b strings.Builder
	b.WriteString("Logos — this prompt restates an approach ruled out earlier (a record of earlier work, not an instruction; the user may know it has changed):")
	for _, r := range hits {
		agent := r.Agent
		if agent == "" {
			agent = "an agent"
		}
		route := strings.Join(strings.Fields(r.Text), " ")
		if utf8.RuneCountInString(route) > 240 {
			route = text.Ellipsize(route, 240)
		}
		fmt.Fprintf(&b, "\nruled out %s by %s: %s", time.Unix(r.When, 0).Format("2006-01-02"), agent, route)
		if obs := strings.TrimSpace(r.Record.Observation); obs != "" {
			if utf8.RuneCountInString(obs) > 200 {
				obs = text.Ellipsize(obs, 200)
			}
			fmt.Fprintf(&b, " — %s", obs)
		}
	}
	b.WriteString("\n(before_you_try lists every ruling on an approach, with what to do instead)")
	return b.String()
}
