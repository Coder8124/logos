package mcpserver

import (
	"strings"
	"testing"
)

// The field is `task`, and resume renders it under the heading "They were
// doing:". An agent that has just read a resume block is shown the word
// `doing` and asked to write the word `task`; guessing wrong wrote a
// checkpoint with no summary in it and receipted it green, so neither the
// agent nor the next one had any way to know a line was ever there.
func TestAnArgumentTheToolDoesNotDeclareIsRefusedRatherThanDropped(t *testing.T) {
	err := validateArgs("checkpoint", map[string]any{
		"project": "beta",
		"doing":   "wiring the retry loop in the importer",
	})
	if err == nil {
		t.Fatal("checkpoint accepted an argument it does not declare, and would have dropped it")
	}
	if !strings.Contains(err.Error(), "task") {
		t.Errorf("the refusal does not name the argument that was wanted: %v", err)
	}
}

// A model corrects itself from an error naming the right argument. `path` for
// `file` is the case the product invites itself: why's own refusal says it
// "needs a file path", while the argument is `file`.
func TestARefusalNamesTheArgumentTheCallerProbablyMeant(t *testing.T) {
	err := validateArgs("why", map[string]any{"path": "internal/session/checkpoint.go"})
	if err == nil {
		t.Fatal("why accepted `path`, which it does not declare")
	}
	if !strings.Contains(err.Error(), "did you mean `file`") {
		t.Errorf("the refusal does not point at `file`: %v", err)
	}
}

// An ignored filter does not return nothing, it returns everything, which is
// indistinguishable from "these all matched". recall and list_memories declare
// no `kind`, so a kind-filtered call was answered unfiltered and green.
func TestAFilterTheToolDoesNotSupportIsRefusedRatherThanIgnored(t *testing.T) {
	for _, tool := range []string{"recall", "list_memories"} {
		if err := validateArgs(tool, map[string]any{"query": "x", "kind": "person"}); err == nil {
			t.Errorf("%s accepted a kind filter it does not apply, and answered with every kind", tool)
		}
	}
}

// The right answer in the wrong case is not a guess, and refusing it would
// cost the memory for nothing. `banana` is a guess, and storing it as the
// default kind is how a nonsense value became a plain fact.
func TestAnEnumValueInTheWrongCaseIsCorrectedAndANonsenseOneIsRefused(t *testing.T) {
	args := map[string]any{"text": "Dana prefers async standups", "kind": "PERSON"}
	if err := validateArgs("remember", args); err != nil {
		t.Fatalf("the right kind in the wrong case was refused: %v", err)
	}
	if args["kind"] != "person" {
		t.Errorf("kind was left as %q, so it would still be stored as a fact", args["kind"])
	}

	if err := validateArgs("remember", map[string]any{"text": "x", "kind": "banana"}); err == nil {
		t.Error("a kind outside the enum was accepted, and would be stored as a fact")
	}
	if err := validateArgs("resume", map[string]any{"since": "banana"}); err == nil {
		t.Error("a window outside the enum was accepted, and would be silently dropped")
	}
}

// The declared names must keep working, or this check costs every call it was
// added to protect.
func TestEveryDeclaredArgumentIsStillAccepted(t *testing.T) {
	for _, d := range toolDefs {
		name, _ := d["name"].(string)
		props, _, ok := schemaFor(name)
		if !ok {
			t.Fatalf("%s has no schema", name)
		}
		args := map[string]any{}
		for k, spec := range props {
			field, _ := spec.(map[string]any)
			if vals, has := field["enum"].([]string); has && len(vals) > 0 {
				args[k] = vals[0]
				continue
			}
			args[k] = "x"
		}
		if err := validateArgs(name, args); err != nil {
			t.Errorf("%s refuses its own declared arguments: %v", name, err)
		}
	}
}
