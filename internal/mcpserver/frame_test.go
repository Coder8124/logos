package mcpserver

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
)

// A client that half-closes stdin after an overlong last request still reads
// stdout; it was owed the -32600 every other overlong frame gets, and the
// server ended without one. The frame ends on a buffer boundary, so the read
// that finds EOF carries no bytes — the only way the bug shows.
func TestAnOverlongFinalFrameWithNoNewlineIsReportedNotTakenForTheEnd(t *testing.T) {
	const size = 64 * 1024
	br := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", maxFrame+size)), size)
	if _, err := readFrame(br); !errors.Is(err, errFrameTooLong) {
		t.Fatalf("overlong final frame: got %v, want errFrameTooLong", err)
	}
	if _, err := readFrame(br); err != io.EOF {
		t.Fatalf("after the overlong frame: got %v, want io.EOF", err)
	}
}
