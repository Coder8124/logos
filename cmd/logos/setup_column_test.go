package main

import (
	"testing"
	"unicode/utf8"

	"github.com/Coder8124/logos/internal/setup"
)

// A name longer than the column pushes that row's ✓ and → out of line with the
// rest, which reads as a report that rendered wrong.
func TestEveryHostNameFitsTheReportColumn(t *testing.T) {
	for _, h := range setup.Hosts() {
		if n := utf8.RuneCountInString(h.Name); n > hostColumn {
			t.Errorf("%q is %d characters; the host column is %d", h.Name, n, hostColumn)
		}
	}
}
