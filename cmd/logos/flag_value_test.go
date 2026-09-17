package main

import "testing"

// #116: a value flag given with no value swallowed the flag that followed it.
// `logos memory add ... --project --kind decision` recorded the project as
// "--kind", and nothing complained — the memory reads as though it were scoped
// and is scoped to a project nobody will ever name again. Invariant 4 in its
// smallest form: the value is missing, so say so by falling back to the
// default, rather than inventing one out of the next word on the line.
func TestAValueFlagWithNoValueDoesNotSwallowTheFlagAfterIt(t *testing.T) {
	args := []string{"--project", "--kind", "decision"}

	if got := flagStr(args, "--project", "fallback"); got != "fallback" {
		t.Errorf("--project with no value took the next flag as its value: %q", got)
	}
	if got := flagStr(args, "--kind", ""); got != "decision" {
		t.Errorf("the flag that was swallowed no longer parses: %q", got)
	}
}

// The same hole in the list form, which feeds --tag and friends.
func TestAListFlagWithNoValueDoesNotSwallowTheFlagAfterIt(t *testing.T) {
	got := flagStrs([]string{"--tag", "--project", "kestrel"}, "--tag")
	if len(got) != 0 {
		t.Errorf("--tag with no value collected the next flag: %q", got)
	}
}

// A single dash is not a flag: it is the conventional name for stdin, and one
// day a path argument will be given it.
func TestASingleDashIsStillAValue(t *testing.T) {
	if got := flagStr([]string{"--path", "-"}, "--path", ""); got != "-" {
		t.Errorf("a lone dash was refused as a value: %q", got)
	}
}
