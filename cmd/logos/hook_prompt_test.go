package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/session"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

const exportRuling = "route: streaming the vendor export row by row | observation: the parser still loads the whole file, memory unchanged | layer: implementation | scope: local | degree: contradicted | action: abandon | alternative: split the export upstream"

// prompt runs the hook as Claude Code would on a submitted prompt, and returns
// the context it injected, or "" when it printed nothing.
func prompt(t *testing.T, vault, repo, sessionID, text string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"session_id": sessionID, "cwd": repo, "hook_event_name": "UserPromptSubmit", "prompt": text,
	})
	var out bytes.Buffer
	promptHookCmd(bytes.NewReader(payload), &out, vault)
	if out.Len() == 0 {
		return ""
	}
	var got struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("the hook printed something Claude Code cannot read: %v\n%s", err, out.String())
	}
	if got.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Errorf("hookEventName = %q, want UserPromptSubmit — Claude Code drops context filed under another event", got.HookSpecificOutput.HookEventName)
	}
	return got.HookSpecificOutput.AdditionalContext
}

// A person typing an approach the project already ruled out is the earliest
// moment the ruling can change anything: before the agent has planned around
// it. Shown once per session, and counted.
func TestAPromptThatRestatesARuledOutApproachIsShownTheRulingOnce(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, exportRuling)

	ask := "The import is too slow. Can you try streaming the vendor export row by row instead?"
	got := prompt(t, vault, repo, "s1", ask)
	if !strings.Contains(got, "streaming the vendor export") || !strings.Contains(got, "by claude-code") {
		t.Fatalf("a prompt restating a ruled-out approach was not shown the ruling:\n%q", got)
	}
	if !strings.Contains(got, "memory unchanged") {
		t.Errorf("the ruling was shown without what was observed, which is the part that changes the plan:\n%s", got)
	}
	if !strings.Contains(got, "not an instruction") {
		t.Errorf("the ruling is not framed as a record — it arrives unasked beside the user's words:\n%s", got)
	}
	if again := prompt(t, vault, repo, "s1", ask); again != "" {
		t.Errorf("the same session was shown the ruling a second time:\n%s", again)
	}
	if other := prompt(t, vault, repo, "s2", ask); other == "" {
		t.Error("a new session was never shown the ruling — once per session, not once ever")
	}

	events, _, err := usagepkg.Read(vault)
	if err != nil {
		t.Fatal(err)
	}
	if n := usagepkg.Sum(events, "").AtPrompt; n != 2 {
		t.Errorf("the usage ledger counts %d rulings shown at the prompt, want 2", n)
	}
}

func TestAPromptAboutSomethingElseIsShownNothing(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, exportRuling)

	if got := prompt(t, vault, repo, "s1", "Please rename the settings page and fix the typo in its heading."); got != "" {
		t.Errorf("an unrelated prompt was shown a ruling:\n%s", got)
	}
}

// Containment divides by the shorter side, so a long prompt holds most of any
// short route by vocabulary alone. The words of the ruling scattered across
// separate sentences are not a restatement of it.
func TestARulingsWordsScatteredAcrossAPromptAreNotARestatement(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, exportRuling)

	scattered := "The vendor changed their pricing page.\n" +
		"Streaming of the demo video is broken on Safari.\n" +
		"Also the export button on the dashboard needs a tooltip.\n" +
		"Finally check row spacing in the table."
	if got := prompt(t, vault, repo, "s1", scattered); got != "" {
		t.Errorf("a prompt that only shares the ruling's words across unrelated sentences was shown it:\n%s", got)
	}
}

// The same scattered words joined by commas are still separate thoughts; a
// clause that ends at a sentence mark only would read them as one proposal.
func TestARulingsWordsScatteredAcrossCommasAreNotARestatement(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, exportRuling)

	scattered := "the vendor changed their pricing page, streaming of the demo video is broken on Safari, " +
		"the export button on the dashboard needs a tooltip, and check row spacing in the table"
	if got := prompt(t, vault, repo, "s1", scattered); got != "" {
		t.Errorf("a comma-joined list of unrelated requests was shown the ruling:\n%s", got)
	}
}

// A person pastes a long log and types the request under it. The request is
// the part that says what they want; a cap that keeps the first clauses reads
// the log and drops the request.
func TestARequestTypedBelowAPastedLogIsStillRead(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, exportRuling)

	var b strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "worker %d processed batch without errors\n", i)
	}
	b.WriteString("Try streaming the vendor export row by row instead.")
	if got := prompt(t, vault, repo, "s1", b.String()); got == "" {
		t.Error("a request typed below a pasted log was not read")
	}
}

// Without a session id there is nowhere to remember the showing, so every
// prompt would carry the same ruling again.
func TestAPromptWithoutASessionIsShownNothing(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, exportRuling)

	if got := prompt(t, vault, repo, "", "Try streaming the vendor export row by row."); got != "" {
		t.Errorf("a prompt with no session id was shown a ruling it can never mark as shown:\n%s", got)
	}
}

// `logos tried --ruled-out` files a ruling as a note before any checkpoint
// exists. A project with no checkpoint directory yet still has that ruling.
func TestARulingFiledOnlyAsANoteIsShownAtThePrompt(t *testing.T) {
	repo, vault, db := editRepo(t)
	if _, err := session.AddNote(db, projectFor(repo), "codex", exportRuling); err != nil {
		t.Fatal(err)
	}
	if got := prompt(t, vault, repo, "s1", "Try streaming the vendor export row by row."); !strings.Contains(got, "streaming the vendor export") {
		t.Errorf("a ruling recorded only as a note was not shown at the prompt:\n%q", got)
	}
}

// The ledger names a ruling by the fingerprint of its whole record, and the
// session-end receipt credits only what this session was shown. An at-prompt
// event with no session, or a ref of the route alone, is never credited.
func TestTheAtPromptLedgerLineNamesTheSessionAndTheRecord(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, exportRuling)
	if got := prompt(t, vault, repo, "s1", "Try streaming the vendor export row by row."); got == "" {
		t.Fatal("the ruling was not shown")
	}
	events, _, err := usagepkg.Read(vault)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Kind != usagepkg.KindAtPrompt {
			continue
		}
		if e.Session != "s1" {
			t.Errorf("the at-prompt event names session %q, want s1", e.Session)
		}
		if len(e.Refs) != 1 || e.Refs[0] != usagepkg.Ref(exportRuling) {
			t.Errorf("the at-prompt event refs %v, want the record's fingerprint %s", e.Refs, usagepkg.Ref(exportRuling))
		}
		return
	}
	t.Fatal("no at-prompt event was recorded")
}

// Containment over the shorter side made a short request contained in any
// longer route that shares two of its words: every "merge the branches" was
// shown the ruling against merging some particular set of branches. A
// restatement has to cover the route, not the other way round.
func TestAShortRequestSharingTwoWordsOfARouteIsNotARestatement(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, "route: merge outstanding branches for the release | observation: every one was written against a package that no longer exists | layer: implementation | scope: local | degree: contradicted | action: abandon | alternative: port the features by hand")

	if got := prompt(t, vault, repo, "s1", "merge the bugfix branches as well"); got != "" {
		t.Errorf("a request to merge some branches was shown the ruling against merging others:\n%s", got)
	}
	if got := prompt(t, vault, repo, "s2", "lets merge the outstanding branches for the release now"); got == "" {
		t.Error("a prompt restating the whole route was not shown it")
	}
}

// before_you_try flags a ruling from another project as elsewhere; nobody
// asked for this one, and a line about another codebase arriving unasked is
// noise.
func TestARulingFromAnotherProjectIsNotShownAtThePrompt(t *testing.T) {
	repo, vault, db := editRepo(t)
	if err := session.Commit(db, vault, &session.Checkpoint{
		Project: "someone-else", Agent: "codex", Task: "work", Next: "carry on",
		Failed: []string{exportRuling},
	}); err != nil {
		t.Fatal(err)
	}
	if got := prompt(t, vault, repo, "s1", "Try streaming the vendor export row by row."); got != "" {
		t.Errorf("a ruling from another project was shown at this project's prompt:\n%s", got)
	}
}

func TestTheRulingsAtPromptSwitchOffSilencesThePromptHook(t *testing.T) {
	repo, vault, db := editRepo(t)
	ruleOut(t, db, vault, repo, exportRuling)
	if err := auto.Set(vault, "rulings-at-prompt", false); err != nil {
		t.Fatal(err)
	}
	if got := prompt(t, vault, repo, "s1", "Try streaming the vendor export row by row."); got != "" {
		t.Errorf("the prompt hook showed a ruling with its switch off:\n%s", got)
	}
}

// The plugin has to call the hook, or none of the above reaches anybody.
func TestThePluginRunsThePromptHookOnEveryPrompt(t *testing.T) {
	raw, err := os.ReadFile("../../plugin/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range cfg.Hooks["UserPromptSubmit"] {
		for _, h := range g.Hooks {
			found = found || strings.HasSuffix(h.Command, "/hooks/ruling-at-prompt.sh")
		}
	}
	if !found {
		t.Fatal("hooks.json does not run ruling-at-prompt.sh on UserPromptSubmit")
	}
	if out, err := exec.Command("bash", "-n", "../../plugin/hooks/ruling-at-prompt.sh").CombinedOutput(); err != nil {
		t.Fatalf("ruling-at-prompt.sh does not parse: %v %s", err, out)
	}
}
