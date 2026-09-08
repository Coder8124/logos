// Package procedure answers the question before_you_try's dead-end half
// leaves unanswered: not just what was ruled out, but what is known to work,
// including the trap in doing it the obvious way.
//
// It is the mirror of internal/deadend on purpose. A dead end stops an agent
// from repeating a mistake; a procedure stops it from repeating the discovery
// that the mistake was avoidable in the first place — "run the chaos tier
// before calling a durability fix done, or go test ./... passes with the bug
// still there" is exactly as valuable as a recorded failure, and exactly as
// likely to be known only to whoever last hit it.
//
// A procedure earns storage only if it carries a trap: something not
// derivable from reading the repository, that cost someone time. Without that
// bar this package would duplicate CONTRIBUTING.md one remembered line at a
// time, which is the failure BRAINPROMPT.md already warns against — "don't
// call remember for what the repository already says."
package procedure

import (
	"fmt"
	"strings"

	"github.com/Coder8124/brain/internal/deadend"
)

// Evidence says how hard the claim that this works has been checked. Distinct
// from deadend.Degree, which grades certainty that something failed — this
// grades certainty that something succeeds, which is a different question
// with a different failure mode: a procedure someone assumes works but never
// ran is the one most likely to waste a reader's time.
type Evidence string

const (
	EvidenceVerified Evidence = "verified" // the author ran Verify and watched it work
	EvidenceOnce     Evidence = "once"     // worked one time; not yet repeated
	EvidenceReported Evidence = "reported" // someone said so; the author did not check
)

func evidenceOf(s string) Evidence {
	switch Evidence(s) {
	case EvidenceVerified, EvidenceOnce, EvidenceReported:
		return Evidence(s)
	default:
		return ""
	}
}

// Record is one procedure, typed. Route and Trap are both required for a
// record to count as typed — see Validate — because a route with no trap is
// a convention rather than a procedure. Layer, Scope, Verify and Evidence are
// each optional and simply absent from Render when unset.
//
// Raw is always populated — the on-disk form, exactly as recorded — so a
// hand-edited or partially-typed entry stays readable rather than silently
// losing whatever did not parse.
type Record struct {
	Route    string
	Trap     string
	Verify   string
	Layer    deadend.Layer // shares deadend's vocabulary: same question, "does this transfer?"
	Scope    deadend.Scope
	Evidence Evidence
	Raw      string
	// Truncated is set when the source entry ran past recordCap and was cut
	// to fit. The cut is visible here rather than silent, per the invariant
	// that a lossy operation says so.
	Truncated bool
}

// recordCap bounds one record at roughly 300 tokens, using the project's own
// chars/4 convention (internal/eval.Tokens) — a before_you_try answer already
// has to fit five other layers into a 4,000-token pack, so no single record
// gets to be the reason it doesn't. Matches deadend.recordCap.
const recordCap = 1200

// ParseRecord turns one stored procedure line into a typed Record.
//
// The wire format mirrors deadend.ParseRecord exactly: a line of `key: value`
// segments joined by " | ", anchored on a `route:` key. Anything else —
// including a line that fails Validate and should never have been stored —
// comes back as free prose with Layer: unclassified, so a caller never has to
// branch on which kind of entry it got.
func ParseRecord(raw string) Record {
	raw = strings.TrimSpace(raw)
	truncated := false
	if len(raw) > recordCap {
		raw = strings.TrimSpace(raw[:recordCap])
		truncated = true
	}

	fields := map[string]string{}
	for _, part := range strings.Split(raw, " | ") {
		key, val, ok := strings.Cut(part, ": ")
		if !ok {
			continue
		}
		fields[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(val)
	}

	route := fields["route"]
	if route == "" {
		return Record{Route: raw, Layer: deadend.LayerUnclassified, Raw: raw, Truncated: truncated}
	}

	return Record{
		Route:     route,
		Trap:      fields["trap"],
		Verify:    fields["verify"],
		Layer:     layerOf(fields["layer"]),
		Scope:     scopeOf(fields["scope"]),
		Evidence:  evidenceOf(fields["evidence"]),
		Raw:       raw,
		Truncated: truncated,
	}
}

// Typed reports whether this parsed as the structured shape at all, rather
// than falling back to free prose.
func (r Record) Typed() bool { return r.Layer != deadend.LayerUnclassified || r.Route != r.Raw }

func layerOf(s string) deadend.Layer {
	switch deadend.Layer(s) {
	case deadend.LayerImplementation, deadend.LayerDesign, deadend.LayerEnvironment,
		deadend.LayerDependency, deadend.LayerRequirement:
		return deadend.Layer(s)
	default:
		return ""
	}
}

func scopeOf(s string) deadend.Scope {
	switch deadend.Scope(s) {
	case deadend.ScopeLocal, deadend.ScopeVersionBound, deadend.ScopeGeneral:
		return deadend.Scope(s)
	default:
		return ""
	}
}

// Validate reports whether a record earns storage as a procedure at all.
//
// The trap requirement is the package's whole editorial policy, made
// mechanical rather than a matter of taste: if a route cannot be written down
// with something that goes wrong without it, it is documentation, and
// documentation belongs in CONTRIBUTING.md, not in a memory a model might
// mistake for something worth repeating uncritically. This is also the one
// guard against the failure the memory-architecture survey calls
// reflective-memory pollution — a single bad write contaminating every
// before_you_try answer downstream — since a required, specific trap is much
// harder to fabricate convincingly than a plausible-sounding tip.
func Validate(r Record) error {
	if strings.TrimSpace(r.Route) == "" {
		return fmt.Errorf("a procedure needs a route: what to do, in one sentence")
	}
	if strings.TrimSpace(r.Trap) == "" {
		return fmt.Errorf("a procedure needs a trap: what goes wrong if you don't do it this way — " +
			"without one this is a convention, not a procedure, and belongs in CONTRIBUTING.md instead")
	}
	return nil
}
