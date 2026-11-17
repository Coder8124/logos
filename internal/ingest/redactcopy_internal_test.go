package ingest

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
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

// A CRLF line's terminator was cut from a body already shortened, so every
// redacted copy of a Windows-written transcript lost its carriage returns and
// no longer extended its own source.
func TestACRLFLineKeepsItsCarriageReturn(t *testing.T) {
	in := "{\"a\":1}\r\n{\"b\":2}\r\n"
	var out bytes.Buffer
	if _, _, err := redactStream(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != in {
		t.Errorf("redacting a clean CRLF transcript changed it: %q", out.String())
	}
}

// A copy identical to today's stream but ending mid-line (a host writing when
// it was read) was taken for one that had grown, and rewritten and counted as
// archived on every run.
func TestACopyIdenticalToTheStreamIsUnchangedEvenMidLine(t *testing.T) {
	dir := t.TempDir()
	tmp, dst := filepath.Join(dir, "tmp"), filepath.Join(dir, "dst")
	body := []byte("{\"a\":1}\n{\"b\":")
	for _, p := range []string{tmp, dst} {
		if err := os.WriteFile(p, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if h := heldRedacted(tmp, dst); h != prefixEqual {
		t.Errorf("heldRedacted = %d, want prefixEqual (%d)", h, prefixEqual)
	}
}
