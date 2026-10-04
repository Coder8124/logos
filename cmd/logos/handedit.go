package main

import (
	"fmt"
	"strings"
)

// announceAdoption says what a hand edit to one of the vault's list files —
// loops.md, the insight queue, the review queue — did to the cache, before the
// command's own output.
//
// The files tell the user that deleting a line forgets the record, and the
// stores honour it by adopting the file before they read or rewrite it. That
// adoption was silent: `loop add` printed only "tracked" on a run that also
// forgot a loop, which is the silent background work invariant 3 rules out.
// Printed only when something changed, so the common case — a file nobody
// touched — adds nothing to the output.
func announceAdoption(file, noun, removedVerb string, restored, removed int) {
	var parts []string
	if removed > 0 {
		parts = append(parts, fmt.Sprintf("%d %s %s", removed, plural(removed, noun), removedVerb))
	}
	if restored > 0 {
		parts = append(parts, fmt.Sprintf("%d %s taken from the file", restored, plural(restored, noun)))
	}
	if len(parts) == 0 {
		return
	}
	fmt.Printf("adopted your edit to %s: %s\n", file, strings.Join(parts, ", "))
}
