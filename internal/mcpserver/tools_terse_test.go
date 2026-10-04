package mcpserver

import (
	"strings"
	"testing"
)

// A host puts every one of these strings in front of its model, and some put
// them in every request. A paragraph per tool read like documentation and cost
// tokens on each turn; a CLI's help gives one line, and so should a tool.
const (
	maxToolDescription  = 200
	maxParamDescription = 120
)

func TestEveryToolDescriptionIsOneShortLine(t *testing.T) {
	for _, def := range toolDefs {
		name, _ := def["name"].(string)
		desc, _ := def["description"].(string)
		if strings.Contains(desc, "\n") || len(desc) > maxToolDescription {
			t.Errorf("%s: description is %d characters, want one line of at most %d:\n%s", name, len(desc), maxToolDescription, desc)
		}
		props, _ := def["inputSchema"].(map[string]any)["properties"].(map[string]any)
		for param, schema := range props {
			pd, _ := schema.(map[string]any)["description"].(string)
			if len(pd) > maxParamDescription {
				t.Errorf("%s.%s: parameter description is %d characters, want at most %d:\n%s", name, param, len(pd), maxParamDescription, pd)
			}
		}
	}
}

// Shortening must not drop the sentences that exist because something went
// wrong without them: a receipt nobody relays is a restore the user never sees,
// a queued memory reported as stored is a false receipt, and a distiller not
// told the citation rule sends claims the filter then drops.
func TestTheShortDescriptionsKeepTheRulesThatWereLearnedTheHardWay(t *testing.T) {
	desc := map[string]string{}
	for _, def := range toolDefs {
		name, _ := def["name"].(string)
		desc[name], _ = def["description"].(string)
	}
	for _, name := range []string{"remember", "forget", "pin_memory", "exclude_memory", "context", "resume", "note_progress", "checkpoint", "handoff", "ingest_harvest", "ingest_distil"} {
		if !strings.HasSuffix(desc[name], relay) {
			t.Errorf("%s returns a receipt but no longer asks the model to relay it", name)
		}
	}
	must := map[string][]string{
		"remember":       {"review"},
		"before_you_try": {"BEFORE"},
		"why":            {"BEFORE"},
		"checkpoint":     {"BEFORE"},
		"ingest_distil":  {"turn", "file edit"},
	}
	for name, words := range must {
		for _, w := range words {
			if !strings.Contains(desc[name], w) {
				t.Errorf("%s description lost %q: %s", name, w, desc[name])
			}
		}
	}
}
