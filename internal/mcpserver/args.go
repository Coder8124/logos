package mcpserver

import (
	"fmt"
	"sort"
	"strings"
)

// Argument checking against the schemas the server already publishes.
//
// Every argument helper below this reads one key and returns a zero value for
// anything absent, and nothing ever looked at the keys that were *not* read.
// So an argument under a name the server does not know was accepted, dropped,
// and receipted green: `checkpoint(doing: "...")` saved a checkpoint with no
// task in it, `why(path: "...")` said it needed a file path while holding one,
// and `recall(kind: "person")` returned every kind — which is worse than
// returning nothing, because a filter that was ignored is indistinguishable
// from a filter that matched everything.
//
// This is invariant 4 in the one place it is hardest to see: the receipt is
// green, so the model cannot detect the failure by reading it. A model does
// correct itself from an error naming the right argument, on the very next
// call, which is why these are refused rather than guessed at.
//
// Deliberately narrower than JSON Schema validation. argBool and argList go out
// of their way to accept the wrong *type* from a loose model, with reasons; a
// wrong type is still usable and a wrong name is gone. Only names and enum
// values are enforced here.

// schemaFor returns the declared properties and required names of a tool.
func schemaFor(tool string) (props map[string]any, required []string, ok bool) {
	for _, d := range toolDefs {
		if d["name"] != tool {
			continue
		}
		schema, _ := d["inputSchema"].(map[string]any)
		props, _ = schema["properties"].(map[string]any)
		req, _ := schema["required"].([]string)
		return props, req, true
	}
	return nil, nil, false
}

// validateArgs refuses a call carrying an argument name the tool does not
// declare, and normalises the case of enum values. It returns nil for a tool
// with no schema rather than inventing a rule for it.
//
// Enum case is corrected rather than refused: "PERSON" where the schema says
// "person" is not a model guessing, it is the right answer in the wrong case,
// and the memory it would file as a plain fact is the same memory either way.
// A value that is not in the enum at all is a guess, and is refused with the
// list — silently storing it as the default is how `kind: "banana"` became a
// fact.
func validateArgs(tool string, args map[string]any) error {
	props, _, ok := schemaFor(tool)
	if !ok || props == nil {
		return nil
	}
	for k, v := range args {
		spec, declared := props[k]
		if !declared {
			return fmt.Errorf("%s has no %s argument%s It takes: %s",
				tool, quoted(k), didYouMean(k, props), strings.Join(names(props), ", "))
		}
		field, _ := spec.(map[string]any)
		vals, has := field["enum"].([]string)
		if !has {
			continue
		}
		s, isStr := v.(string)
		if !isStr || strings.TrimSpace(s) == "" {
			continue
		}
		canon, valid := matchEnum(strings.TrimSpace(s), vals)
		if !valid {
			return fmt.Errorf("%s does not take %s=%s. It takes: %s",
				tool, k, quoted(s), strings.Join(vals, ", "))
		}
		args[k] = canon
	}
	return nil
}

// matchEnum resolves a value to its declared spelling, ignoring case.
func matchEnum(got string, vals []string) (string, bool) {
	for _, v := range vals {
		if strings.EqualFold(got, v) {
			return v, true
		}
	}
	return "", false
}

func names(props map[string]any) []string {
	out := make([]string, 0, len(props))
	for k := range props {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func quoted(s string) string { return "`" + s + "`" }

// didYouMean names the closest declared argument, when there is one close
// enough to be a misremembering rather than a different idea. The cases this
// exists for are real and specific: `doing` for `task`, because resume renders
// task under the heading "They were doing:", and `path` for `file`.
func didYouMean(got string, props map[string]any) string {
	best, bestD := "", 0
	for _, k := range names(props) {
		d := editDistance(strings.ToLower(got), strings.ToLower(k))
		// Half the name may differ, but never more, or every short argument
		// name is "close" to every other one and the suggestion is noise.
		if d*2 > len(k) {
			continue
		}
		if best == "" || d < bestD {
			best, bestD = k, d
		}
	}
	// A synonym shares no letters with the word it stands in for, so distance
	// cannot find it. These are the ones the product's own output invites.
	if best == "" {
		if alt, aliased := argAliases[got]; aliased {
			if _, declared := props[alt]; declared {
				best = alt
			}
		}
	}
	if best == "" {
		return "."
	}
	return " — did you mean " + quoted(best) + "?"
}

// argAliases maps a word the product itself uses for a field to the argument
// name that field actually has. Each one is a name the server printed to an
// agent and then refused to accept back.
var argAliases = map[string]string{
	"doing": "task", // resume renders task under "They were doing:"
	"path":  "file", // why takes file, and every error about it says "file path"
	"note":  "text", // note_progress's own name says note
	"body":  "text",
	"name":  "project",
}

// editDistance is Levenshtein, over argument names that are one short word
// each, so the quadratic table costs nothing at this size.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
