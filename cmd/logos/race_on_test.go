//go:build race

package main

// raceEnabled is whether this test binary was built with -race. CI runs the
// Linux suite that way; a wall-clock budget measured there times the race
// detector's instrumentation, several times slower, not the hook a user runs.
const raceEnabled = true
