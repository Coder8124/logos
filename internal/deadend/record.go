package deadend

import (
	"strings"
	"time"
)

// Layer names where in the stack a dead end lives. It is the first question
// anyone re-attempting the approach actually needs answered: a ruling about a
// dependency's behaviour transfers across projects that share the dependency;
// one about this project's design does not transfer at all.
type Layer string

const (
	LayerImplementation Layer = "implementation"
	LayerDesign         Layer = "design"
	LayerEnvironment    Layer = "environment"
	LayerDependency     Layer = "dependency"
	LayerRequirement    Layer = "requirement"
	// LayerUnclassified is not a fifth peer alongside the five above — it is
	// what an ordinary, free-prose Failed entry gets, so every checkpoint
	// written before this schema existed keeps reading exactly as it did.
	LayerUnclassified Layer = "unclassified"
)

// Scope says how far a ruling travels. The distinction Check already draws
// between "this project" and "elsewhere" is about where a ruling was made;
// Scope is about where it still applies, which is a different question — a
// version-bound ruling may no longer hold even on the project that made it.
type Scope string

const (
	ScopeLocal        Scope = "local"         // true of this codebase's particular choices, not the approach in general
	ScopeVersionBound Scope = "version-bound" // true against a dependency version that can move out from under it
	ScopeGeneral      Scope = "general"       // true of the approach itself, not tied to a version or a local choice
)

// Degree says how hard the evidence is. "Failed" covers everything from "the
// spec forbids it" to "it flaked once and nobody chased why", and those
// deserve different weight from a reader deciding whether to retry.
type Degree string

const (
	DegreeContradicted Degree = "contradicted" // demonstrated not to work
	DegreePartial      Degree = "partial"      // worked for part of the case, not all of it
	DegreeInconclusive Degree = "inconclusive" // tried, no clear result either way
	DegreeUnstable     Degree = "unstable"     // worked sometimes, not reliably
)

// Action is what the person who found this out recommends doing with that
// knowledge, distinct from Alternative, which is what to do instead.
type Action string

const (
	ActionRetry        Action = "retry"         // worth trying again — conditions may have changed
	ActionChangeMethod Action = "change-method" // the goal is fine, this route to it is not
	ActionNarrowScope  Action = "narrow-scope"  // works, but only for a smaller case than proposed
	ActionAbandon      Action = "abandon"       // the goal itself should be dropped
)

// Record is one dead end, typed. Route and Observation are required for a
// record to count as typed at all; Layer/Scope/Degree/Action/Alternative are
// each optional and simply absent from Render when unset.
//
// Raw is always populated — the on-disk form, exactly as recorded — because a
// hand-edited or partially-typed entry should still be readable rather than
// silently losing whatever did not parse.
type Record struct {
	Route       string
	Observation string
	Layer       Layer
	Scope       Scope
	Degree      Degree
	Action      Action
	Alternative string
	Raw         string
	// Truncated is set when the source entry ran past recordCap and was cut
	// to fit. The cut is visible here rather than silent, per the invariant
	// that a lossy operation says so.
	Truncated bool
}

// recordCap bounds one record at roughly 300 tokens, using the project's own
// chars/4 convention (internal/eval.Tokens) — a before_you_try answer already
// has to fit five other layers into a 4,000-token pack, so no single record
// gets to be the reason it doesn't.
const recordCap = 1200

// ParseRecord turns one Failed or working-note entry into a typed Record.
//
// The wire format is unchanged — Failed is still `[]string`, and a record is
// still one bullet, one line — so the shape lives entirely in the text: a
// line of `key: value` segments joined by " | ", anchored on a `route:` key.
// Anything else, including every Failed entry ever written before this
// existed, is not that shape and comes back as free prose with
// Layer: unclassified — the same record type, so a caller never has to
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
		return Record{Route: raw, Layer: LayerUnclassified, Raw: raw, Truncated: truncated}
	}

	return Record{
		Route:       route,
		Observation: fields["observation"],
		Layer:       layerOf(fields["layer"]),
		Scope:       scopeOf(fields["scope"]),
		Degree:      degreeOf(fields["degree"]),
		Action:      actionOf(fields["action"]),
		Alternative: fields["alternative"],
		Raw:         raw,
		Truncated:   truncated,
	}
}

// Typed reports whether this parsed as the structured shape at all, rather
// than falling back to free prose. Used to decide whether Layer/Scope/etc are
// worth rendering.
func (r Record) Typed() bool { return r.Layer != LayerUnclassified || r.Route != r.Raw }

func layerOf(s string) Layer {
	switch Layer(s) {
	case LayerImplementation, LayerDesign, LayerEnvironment, LayerDependency, LayerRequirement:
		return Layer(s)
	default:
		return ""
	}
}

func scopeOf(s string) Scope {
	switch Scope(s) {
	case ScopeLocal, ScopeVersionBound, ScopeGeneral:
		return Scope(s)
	default:
		return ""
	}
}

func degreeOf(s string) Degree {
	switch Degree(s) {
	case DegreeContradicted, DegreePartial, DegreeInconclusive, DegreeUnstable:
		return Degree(s)
	default:
		return ""
	}
}

func actionOf(s string) Action {
	switch Action(s) {
	case ActionRetry, ActionChangeMethod, ActionNarrowScope, ActionAbandon:
		return Action(s)
	default:
		return ""
	}
}

// staleAfter is how long a version-bound ruling gets to stand unquestioned.
// Past this, the dependency it was made against has had a fair chance to
// move, so the ruling is surfaced as possibly superseded rather than dropped
// — the paper this schema adapts from leaves staleness unsolved entirely, and
// a wrong dead end that still gets shown with a caveat is safer than one that
// silently stops applying.
const staleAfter = 90 * 24 * time.Hour

// PossiblySuperseded reports whether a version-bound ruling is old enough
// that the dependency it names may have moved since. It is a time-based
// guess, not a check against an actual manifest — Logos has no dependency
// graph to consult — so it is always surfaced as a caveat, never a silent
// drop and never a hard "this no longer applies".
func PossiblySuperseded(scope Scope, when int64, now time.Time) bool {
	if scope != ScopeVersionBound || when == 0 {
		return false
	}
	return now.Sub(time.Unix(when, 0)) > staleAfter
}
