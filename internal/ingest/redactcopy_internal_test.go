package ingest

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type failingReader struct {
	head string
	done bool
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.done {
		return 0, errors.New("disk went away")
	}
	f.done = true
	return copy(p, f.head), nil
}

// A read that dies after some lines were redacted must surface as an error,
// because ArchiveTranscript's refusal to keep a raw copy only fires on one: a
// swallowed mid-stream failure would rename a truncated temp file into place as
// if it were the whole transcript.
func TestARedactionThatFailsMidStreamReportsTheFailure(t *testing.T) {
	_, _, err := redactStream(&failingReader{head: `{"type":"user","message":"x"}` + "\n" + "second line"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "disk went away") {
		t.Fatalf("redactStream err = %v, want the read failure", err)
	}
}
