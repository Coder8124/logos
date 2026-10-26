package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/ops"
	"github.com/Coder8124/logos/internal/session"
)

// conforms checks v against one of docs/logos.schema.json's definitions, for
// the parts of JSON Schema that file uses. A schema nothing checks drifts from
// the output the first time a field is added, and the reader who trusted it
// finds out in production.
func conforms(t *testing.T, def string, out string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../../docs/logos.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("--json printed something that is not one JSON document: %v\n%s", err, out)
	}
	defs := schema["$defs"].(map[string]any)
	for _, problem := range check(defs, defs[def].(map[string]any), v, def) {
		t.Error(problem)
	}
	for _, k := range goNamedKeys(v, def) {
		t.Errorf("%s: a struct with no json tags leaked its Go field name", k)
	}
	return v
}

// goNamedKeys finds keys anywhere in v that are not snake_case. The schema
// types a checkpoint or a ruling only as an object, so it cannot catch a nested
// struct marshalled with no tags — "Next", "Route" — and a reader written
// against the documented names finds every field missing.
func goNamedKeys(v any, at string) []string {
	var bad []string
	switch v := v.(type) {
	case map[string]any:
		for k, e := range v {
			if strings.ToLower(k) != k {
				bad = append(bad, at+"."+k)
			}
			bad = append(bad, goNamedKeys(e, at+"."+k)...)
		}
	case []any:
		for i, e := range v {
			bad = append(bad, goNamedKeys(e, fmt.Sprintf("%s[%d]", at, i))...)
		}
	}
	return bad
}

func check(defs, s map[string]any, v any, at string) []string {
	var bad []string
	if ref, ok := s["$ref"].(string); ok {
		return check(defs, defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any), v, at)
	}
	for _, sub := range asList(s["allOf"]) {
		bad = append(bad, check(defs, sub.(map[string]any), v, at)...)
	}
	if enum, ok := s["enum"].([]any); ok && !slices.Contains(enum, v) {
		bad = append(bad, fmt.Sprintf("%s: %v is not one of %v", at, v, enum))
	}
	if c, ok := s["const"]; ok && c != v {
		bad = append(bad, fmt.Sprintf("%s: %v, want %v", at, v, c))
	}
	switch s["type"] {
	case "array":
		list, ok := v.([]any)
		if !ok {
			// null is the case this exists for: an empty list marshalled from
			// a nil slice is not an empty list to a strict reader.
			return append(bad, fmt.Sprintf("%s: %v is not an array", at, v))
		}
		if items, ok := s["items"].(map[string]any); ok {
			for i, e := range list {
				bad = append(bad, check(defs, items, e, fmt.Sprintf("%s[%d]", at, i))...)
			}
		}
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return append(bad, fmt.Sprintf("%s: %v is not an object", at, v))
		}
	case "integer":
		if f, ok := v.(float64); !ok || f != float64(int(f)) {
			bad = append(bad, fmt.Sprintf("%s: %v is not an integer", at, v))
		}
	case "string":
		if _, ok := v.(string); !ok {
			bad = append(bad, fmt.Sprintf("%s: %v is not a string", at, v))
		}
	}
	if min, ok := s["minItems"].(float64); ok {
		if list, _ := v.([]any); len(list) < int(min) {
			bad = append(bad, fmt.Sprintf("%s: %d items, want at least %v", at, len(list), min))
		}
	}
	obj, _ := v.(map[string]any)
	props, _ := s["properties"].(map[string]any)
	for _, r := range asList(s["required"]) {
		if _, ok := obj[r.(string)]; !ok {
			bad = append(bad, fmt.Sprintf("%s: %q is required and missing", at, r))
		}
	}
	for k, val := range obj {
		sub, known := props[k].(map[string]any)
		if !known {
			if s["additionalProperties"] == false {
				bad = append(bad, fmt.Sprintf("%s: %q is not in the schema", at, k))
			}
			continue
		}
		bad = append(bad, check(defs, sub, val, at+"."+k)...)
	}
	return bad
}

func asList(v any) []any { l, _ := v.([]any); return l }

func outcomeOf(v map[string]any) map[string]any { return v["outcome"].(map[string]any) }

func TestRecallWithJsonPrintsTheMemoriesAndAnOutcomeBlockTheSchemaDescribes(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	t.Setenv("LOGOS_EMBED", "off")
	ix, err := openIndex()
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Store(ix.DB, nil, "", &memory.Memory{
		Text: "The tariff table is refreshed nightly from the regulator's feed.", Kind: memory.Fact, Project: "kestrel",
	}); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	out := captureStdout(t, func() {
		if err := recallCmd([]string{"tariff", "table", "--project", "kestrel", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	v := conforms(t, "recall", out)
	if v["query"] != "tariff table" {
		t.Errorf("--json leaked into the query, or the query was lost: %v", v["query"])
	}
	o := outcomeOf(v)
	if o["status"] != ops.StatusOK || o["returned"] != 1.0 {
		t.Errorf("one memory matched; the block says %v", o)
	}
	if s := o["searched"].([]any); len(s) != 1 || s[0].(map[string]any)["project"] != "kestrel" {
		t.Errorf("searched should name the project recall was scoped to: %v", o["searched"])
	}
}

// The whole reason the block exists. A memory list with nothing in it is the
// same JSON whether the vault had no match or could not be read, and a
// binding that renders both as "no memories" tells an agent to go ahead when
// it should stop.
func TestAFailedLookupIsNeverRenderedAsAnEmptyListInJson(t *testing.T) {
	for _, cmd := range jsonVerbs {
		out := captureStdout(t, func() {
			printJSONFailure(cmd, []string{"x", "--json"}, errors.New("the index could not be opened"))
		})
		v := conforms(t, "failure", out)
		if _, listed := v["memories"]; listed {
			t.Errorf("%s: a failure carried a result list beside it: %s", cmd, out)
		}
		if got := outcomeOf(v)["errors"].([]any); len(got) != 1 || got[0] != "the index could not be opened" {
			t.Errorf("%s: the error did not reach the block: %v", cmd, got)
		}
	}
	if out := captureStdout(t, func() { printJSONFailure("recall", []string{"x"}, errors.New("no")) }); out != "" {
		t.Errorf("a failure without --json printed JSON to stdout: %q", out)
	}
}

func TestANothingFoundRecallIsEmptyWithAnArrayNotNull(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_EMBED", "off")
	out := captureStdout(t, func() {
		if err := recallCmd([]string{"anything", "--all-projects", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	v := conforms(t, "recall", out)
	if o := outcomeOf(v); o["status"] != ops.StatusEmpty || o["searched"].([]any)[0].(map[string]any)["all_projects"] != true {
		t.Errorf("nothing matched across every project; the block says %v", o)
	}
}

// Resume prints its announcements — a recovered checkpoint, the review queue —
// around the pack. Printed that way under --json they make stdout two
// documents and some prose, which no parser reads.
func TestResumeWithJsonIsOneDocumentWithTheCheckpointAndItsAnnouncementsInside(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	t.Setenv("LOGOS_EMBED", "off")
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, vault, &session.Checkpoint{Project: "kestrel", Next: "refresh the tariff table"}); err != nil {
		t.Fatal(err)
	}
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Store(ix.DB, nil, vault, &memory.Memory{Text: "Tariffs are in pence.", Kind: memory.Fact, Project: "kestrel", Quarantined: true}); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	out := captureStdout(t, func() {
		if err := runResume([]string{"kestrel", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	v := conforms(t, "pack", out)
	if cp, _ := v["checkpoint"].(map[string]any); cp == nil || cp["next"] != "refresh the tariff table" {
		t.Errorf("the handoff is the point of resume and is not in the JSON: %v", v["checkpoint"])
	}
	o := outcomeOf(v)
	if o["status"] != ops.StatusOK || o["returned"].(float64) < 1 {
		t.Errorf("a checkpoint was restored; the block says %v", o)
	}
	notes := fmt.Sprint(o["notes"])
	if !strings.Contains(notes, "review") {
		t.Errorf("a proposal is waiting and the block did not say so: %v", o["notes"])
	}
}

func TestContextWithJsonCountsWhatTheBudgetHeldBackAsOmitted(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	t.Setenv("LOGOS_EMBED", "off")
	t.Setenv("LOGOS_RUNTIME", "none")
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, vault, &session.Checkpoint{Project: "kestrel", Task: "the tariff refresh"}); err != nil {
		t.Fatal(err)
	}
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		text := fmt.Sprintf("Tariff refresh rule %d: ", i) + strings.Repeat(fmt.Sprintf("the refresh reads feed %d before the table. ", i), 8)
		if _, err := memory.Store(ix.DB, nil, vault, &memory.Memory{Text: text, Kind: memory.Fact, Project: "kestrel"}); err != nil {
			t.Fatal(err)
		}
	}
	ix.Close()

	out := captureStdout(t, func() {
		if err := runContext([]string{"the tariff refresh", "--project", "kestrel", "--budget", "120", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	v := conforms(t, "pack", out)
	o := outcomeOf(v)
	omitted := o["omitted"].(map[string]any)
	if omitted["budget"].(float64) == 0 || o["status"] != ops.StatusPartial {
		t.Errorf("twelve long memories into 120 tokens must drop some, and say so: %v", o)
	}
	if !strings.Contains(v["pack"].(string), "Tariff refresh rule") {
		t.Errorf("the rendered pack is missing: %v", v["pack"])
	}
}

func TestTriedWithJsonReturnsTheRulingAndSearchesEveryProject(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_EMBED", "off")
	if err := runTried([]string{"cache the embeddings in the index", "--ruled-out", "a reindex drops them", "--project", "kestrel"}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := runTried([]string{"cache the embeddings in the index", "--project", "kestrel", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	v := conforms(t, "tried", out)
	o := outcomeOf(v)
	if o["returned"] != 1.0 || o["status"] != ops.StatusOK {
		t.Errorf("the approach was ruled out a moment ago; the block says %v", o)
	}
	if o["searched"].([]any)[0].(map[string]any)["all_projects"] != true {
		t.Errorf("tried reads every project's dead ends and must say so: %v", o["searched"])
	}
	// No model is configuration, not a fault: a note, and the status stays
	// what the lookup found.
	if !strings.Contains(fmt.Sprint(o["notes"]), "wording only") {
		t.Errorf("a check by wording alone has to say so: %v", o["notes"])
	}
}

func TestTriedRefusesJsonWhenRecordingRatherThanAnsweringInText(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	if err := runTried([]string{"x", "--ruled-out", "y", "--project", "kestrel", "--json"}); err == nil {
		t.Fatal("recording with --json printed a text receipt a JSON caller cannot read")
	}
}
