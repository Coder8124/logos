package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func listedTools(t *testing.T, s *Session) []string {
	t.Helper()
	resp := s.handle(request{ID: json.RawMessage("1"), Method: "tools/list"})
	b, _ := json.Marshal(resp.Result)
	var out struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range out.Tools {
		names = append(names, tl.Name)
	}
	return names
}

// A host that puts every tool definition into every model request pays for all
// seventeen on every turn — 3,926 tokens measured (#61) — whether or not the
// agent will ever call ingest_distil. The continuity set is what an agent needs
// mid-task, and it is the only thing such a host should be charged for.
func TestTheContinuityToolSetListsOnlyTheToolsAnAgentNeedsMidTask(t *testing.T) {
	srv, _ := testServer(t)
	if err := srv.SetTools("continuity"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(listedTools(t, &Session{Server: srv}), ",")
	want := "remember,recall,context,resume,before_you_try,why,note_progress,checkpoint,handoff,ingest_harvest,ingest_distil"
	if got != want {
		t.Errorf("continuity set lists\n  %s\nwant\n  %s", got, want)
	}
}

func TestWithNoToolSetChosenEveryToolIsListed(t *testing.T) {
	srv, _ := testServer(t)
	if n := len(listedTools(t, &Session{Server: srv})); n != len(toolDefs) {
		t.Errorf("listed %d tools, want all %d", n, len(toolDefs))
	}
}

// A tool the host was never shown can still be called by name — a receipt that
// says "call forget" does not know which set is served. Answering it anyway
// would make the set a display filter, not a choice; failing silently would be
// worse. It refuses, and says how to get the tool.
func TestCallingAToolOutsideTheServedSetSaysHowToGetIt(t *testing.T) {
	srv, _ := testServer(t)
	if err := srv.SetTools("continuity"); err != nil {
		t.Fatal(err)
	}
	resp := (&Session{Server: srv}).handle(request{
		ID: json.RawMessage("1"), Method: "tools/call",
		Params: json.RawMessage(`{"name":"forget","arguments":{"id":1}}`),
	})
	b, _ := json.Marshal(resp)
	if !strings.Contains(string(b), `"isError":true`) || !strings.Contains(string(b), "--tools all") {
		t.Errorf("a call outside the served set was not refused with a way to get it: %s", b)
	}
}

// A typo in the host config must not quietly serve everything, or nothing.
func TestAnUnknownToolSetIsRefused(t *testing.T) {
	srv, _ := testServer(t)
	if err := srv.SetTools("continuty"); err == nil {
		t.Error("an unknown tool set was accepted")
	}
}

// `logos ingest` and `logos bootstrap` only harvest, then tell the user to ask
// a connected agent to run ingest_distil — no terminal command can distil. A
// continuity server that refused it would send that agent back to a terminal
// that cannot do the job, while the transcripts it needs rotate away.
func TestTheContinuityToolSetKeepsDistillationBecauseOnlyAnAgentCanDoIt(t *testing.T) {
	srv, _ := testServer(t)
	if err := srv.SetTools("continuity"); err != nil {
		t.Fatal(err)
	}
	listed := "," + strings.Join(listedTools(t, &Session{Server: srv}), ",") + ","
	for _, name := range []string{"ingest_harvest", "ingest_distil"} {
		if !strings.Contains(listed, ","+name+",") {
			t.Errorf("continuity set does not serve %s, which no terminal command replaces", name)
		}
	}
}
