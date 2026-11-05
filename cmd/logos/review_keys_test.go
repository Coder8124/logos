package main

import (
	"bufio"
	"strings"
	"testing"
)

// fakeRaw stands in for the terminal's raw mode and counts how often it was
// entered and left, so a test can see the terminal is always given back.
func fakeRaw(entered, restored *int) func() (func(), error) {
	return func() (func(), error) {
		*entered++
		return func() { *restored++ }, nil
	}
}

func answersTo(t *testing.T, keys string, n int) []string {
	t.Helper()
	var entered, restored int
	answer := keyAnswers(bufio.NewReader(strings.NewReader(keys)), fakeRaw(&entered, &restored))
	var got []string
	captureStdout(t, func() {
		for range n {
			k, err := answer()
			if err != nil {
				t.Fatalf("answer %d: %v", len(got)+1, err)
			}
			got = append(got, k)
		}
	})
	if entered != n || restored != n {
		t.Fatalf("raw mode entered %d, restored %d, for %d answers", entered, restored, n)
	}
	return got
}

func TestAtATerminalOneKeypressAnswersAReviewPromptWithoutEnter(t *testing.T) {
	got := answersTo(t, "arsY", 4)
	if strings.Join(got, ",") != "a,r,s,y" {
		t.Fatalf("got %q", got)
	}
}

func TestEnterAloneSkipsInASingleKeyReview(t *testing.T) {
	if got := answersTo(t, "\r", 1); got[0] != "" {
		t.Fatalf("Enter answered %q, want a skip", got[0])
	}
}

func TestAnArrowKeyInReviewIsNotReadAsAccept(t *testing.T) {
	// Up is ESC [ A: byte by byte, its A would accept a memory.
	if got := answersTo(t, "\x1b[A\x1b[Br", 1); got[0] != "r" {
		t.Fatalf("an arrow key answered %q", got[0])
	}
}

func TestCtrlCInASingleKeyReviewQuitsWithTheTally(t *testing.T) {
	if got := answersTo(t, "\x03", 1); got[0] != "q" {
		t.Fatalf("Ctrl-C answered %q, want q", got[0])
	}
}

func TestKeysThatAnswerNothingAreSwallowed(t *testing.T) {
	if got := answersTo(t, "xz9 a", 1); got[0] != "a" {
		t.Fatalf("got %q, want a", got[0])
	}
}

func TestTheTerminalIsGivenBackWhenInputEndsMidReview(t *testing.T) {
	var entered, restored int
	answer := keyAnswers(bufio.NewReader(strings.NewReader("x")), fakeRaw(&entered, &restored))
	if _, err := answer(); err == nil {
		t.Fatal("no error at the end of input")
	}
	if restored != entered {
		t.Fatalf("raw mode entered %d, restored %d", entered, restored)
	}
}
