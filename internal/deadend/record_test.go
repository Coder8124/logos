package deadend

import (
	"strings"
	"testing"
	"time"
)

// The whole point of the schema: a record built from every typed field parses
// back out with each one intact.
func TestParseRecordRecognizesEveryTypedField(t *testing.T) {
	raw := "route: switch to grpc-web | observation: browser blocked the preflight with CORS | " +
		"layer: environment | scope: version-bound | degree: contradicted | action: abandon | " +
		"alternative: put an envoy proxy in front of it"
	r := ParseRecord(raw)

	if !r.Typed() {
		t.Fatal("a route-anchored record should report itself as typed")
	}
	if r.Route != "switch to grpc-web" {
		t.Errorf("route = %q", r.Route)
	}
	if r.Observation != "browser blocked the preflight with CORS" {
		t.Errorf("observation = %q", r.Observation)
	}
	if r.Layer != LayerEnvironment {
		t.Errorf("layer = %q", r.Layer)
	}
	if r.Scope != ScopeVersionBound {
		t.Errorf("scope = %q", r.Scope)
	}
	if r.Degree != DegreeContradicted {
		t.Errorf("degree = %q", r.Degree)
	}
	if r.Action != ActionAbandon {
		t.Errorf("action = %q", r.Action)
	}
	if r.Alternative != "put an envoy proxy in front of it" {
		t.Errorf("alternative = %q", r.Alternative)
	}
}

// Every Failed entry ever written before this schema existed is plain prose
// with no "route:" key. It must come back readable, not rejected — the
// backward-compatibility promise the plan makes by name.
func TestParseRecordFallsBackToUnclassifiedProse(t *testing.T) {
	raw := "Switching to a plastic frame — fails the drop test at 1.2m"
	r := ParseRecord(raw)

	if r.Typed() {
		t.Error("plain prose with no route: key should not report itself as typed")
	}
	if r.Layer != LayerUnclassified {
		t.Errorf("layer = %q, want unclassified", r.Layer)
	}
	if r.Route != raw {
		t.Errorf("route = %q, want the whole raw string preserved", r.Route)
	}
}

// An unrecognised value in a typed field must not become a fabricated enum
// member — it is simply absent, same as a field the caller never mentioned.
func TestParseRecordIgnoresAnUnrecognisedEnumValue(t *testing.T) {
	r := ParseRecord("route: try it again | layer: quantum-mechanics | scope: local")
	if r.Layer != "" {
		t.Errorf("layer = %q, want empty for an unrecognised value", r.Layer)
	}
	if r.Scope != ScopeLocal {
		t.Errorf("scope = %q, want local to still parse", r.Scope)
	}
}

// The cap is what keeps a record from spending a disproportionate share of a
// before_you_try answer's budget, and a cut this large must say so rather
// than silently losing the tail of what was recorded.
func TestParseRecordCapsAnOversizedEntry(t *testing.T) {
	huge := "route: " + strings.Repeat("x", 2000)
	r := ParseRecord(huge)
	if !r.Truncated {
		t.Error("an entry past recordCap should report itself as truncated")
	}
	if len(r.Raw) > recordCap {
		t.Errorf("raw len = %d, want <= %d", len(r.Raw), recordCap)
	}
}

// A record within the cap must not be touched — truncation is a last resort,
// not a default posture.
func TestParseRecordLeavesAnOrdinaryEntryAlone(t *testing.T) {
	r := ParseRecord("route: keep it short | observation: fine")
	if r.Truncated {
		t.Error("an ordinary-length entry should not be marked truncated")
	}
}

// possiblySuperseded is a caveat, not a verdict — it fires only for the one
// combination the plan calls out: version-bound, and old enough that the
// dependency it names may have moved.
func TestPossiblySupersededOnlyFlagsAnOldVersionBoundRuling(t *testing.T) {
	now := time.Now()
	old := now.Add(-100 * 24 * time.Hour).Unix()
	recent := now.Add(-10 * 24 * time.Hour).Unix()

	if !possiblySuperseded(ScopeVersionBound, old, now) {
		t.Error("a 100-day-old version-bound ruling should be flagged")
	}
	if possiblySuperseded(ScopeVersionBound, recent, now) {
		t.Error("a 10-day-old version-bound ruling should not be flagged")
	}
	if possiblySuperseded(ScopeLocal, old, now) {
		t.Error("scope:local must never be flagged, no matter the age")
	}
	if possiblySuperseded(ScopeGeneral, old, now) {
		t.Error("scope:general must never be flagged, no matter the age")
	}
	if possiblySuperseded(ScopeVersionBound, 0, now) {
		t.Error("an entry with no timestamp has no gap to reason about")
	}
}
