package eval

import (
	"fmt"
	"strings"
)

// Options controls one benchmark run.
type Options struct {
	// Only runs the scenarios whose id, family or skill contains this string.
	Only string
	// Progress is called before each scenario.
	Progress func(adapter, scenario string, done, total int)
	// Trace, if set, receives the raw response alongside its score. The suite is
	// only as good as its gold labels, and the only way to tell a real pass from
	// a substring that happened to appear is to read what the system actually
	// returned.
	Trace func(sc Scenario, resp Response, score Score)
}

// Select filters a suite by the Only string.
func Select(suite []Scenario, only string) []Scenario {
	if strings.TrimSpace(only) == "" {
		return suite
	}
	only = strings.ToLower(only)
	var out []Scenario
	for _, s := range suite {
		if strings.Contains(strings.ToLower(s.ID), only) ||
			strings.Contains(strings.ToLower(s.Family), only) ||
			strings.Contains(strings.ToLower(s.Skill), only) {
			out = append(out, s)
		}
	}
	return out
}

// Run puts one adapter through a suite and returns a score per scenario.
//
// Every scenario starts from Reset, so no system can score by carrying state
// between cases — a store that answered the last question well should get no
// help on the next one. Setup events are delivered in timestamp order, exactly
// as written, to every adapter alike.
func Run(ad Adapter, suite []Scenario, opt Options) ([]Score, error) {
	scores := make([]Score, 0, len(suite))

	for i, sc := range suite {
		if opt.Progress != nil {
			opt.Progress(ad.Name(), sc.ID, i+1, len(suite))
		}
		if err := ad.Reset(); err != nil {
			return nil, fmt.Errorf("%s: reset before %s: %w", ad.Name(), sc.ID, err)
		}

		// A write failure is the adapter's problem, not the harness's: record it
		// as a failed case and keep going, so one brittle system cannot abort a
		// comparison run that the others would have completed.
		var writeErr error
		for _, ev := range sc.Setup {
			if err := ad.Write(ev); err != nil {
				writeErr = fmt.Errorf("write %s: %w", ev.Kind, err)
				break
			}
		}
		if writeErr != nil {
			scores = append(scores, grade(sc, ad.Name(), Response{Err: writeErr}))
			continue
		}

		resp := readFor(ad, sc)
		score := grade(sc, ad.Name(), resp)
		if opt.Trace != nil {
			opt.Trace(sc, resp, score)
		}
		scores = append(scores, score)
	}
	return scores, nil
}

// readFor performs the scenario's read, including the durability drop.
//
// A system that does not implement Durable has no source of truth outside its
// own index, so wiping the derived state wipes everything. The harness records
// that as an empty response rather than skipping the case. This is the one
// place the suite asserts an outcome instead of measuring it, and it is stated
// plainly in the report for that reason — the alternative, excusing those
// systems from the family, would hide the very property being tested.
func readFor(ad Adapter, sc Scenario) Response {
	if sc.DropDerived {
		d, ok := ad.(Durable)
		if !ok {
			return Response{Err: errNoSourceOfTruth}
		}
		if err := d.DropDerived(); err != nil {
			return Response{Err: fmt.Errorf("dropping derived state: %w", err)}
		}
	}
	resp, err := ad.Read(sc.Query)
	if err != nil {
		resp.Err = err
	}
	return resp
}

var errNoSourceOfTruth = fmt.Errorf("no source of truth outside the cache: dropping derived state loses everything")

// ErrNoSourceOfTruth reports whether a score failed because the system had
// nothing left after its cache was dropped.
func ErrNoSourceOfTruth(err error) bool { return err == errNoSourceOfTruth }
