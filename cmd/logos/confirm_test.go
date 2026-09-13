package main

import (
	"os"
	"testing"
)

// Each prompt used to open its own scanner on stdin. The first one buffered
// the whole pipe, so a scripted `printf 'n\ny\n' | logos setup` answered the
// first question and then hit "no answer (not a terminal)" on the second, with
// the y sitting unread in a scanner nobody would call again.
func TestEveryPipedAnswerReachesItsOwnPrompt(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("n\ny\n"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close() })

	var first, second bool
	captureStdout(t, func() {
		first = confirm("pull the model?")
		second = confirm("wire 1 host(s)?")
	})
	if first {
		t.Error("the first prompt was answered n and took it as yes")
	}
	if !second {
		t.Error("the second prompt never got its piped y")
	}
}
