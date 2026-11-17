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

// A release whose detectors did not know a token's shape left it in the copy,
// the copy matches its source on that line, and the quick path trusted it: an
// upgrade never masked it. A stamp from another release is a full pass.
func TestACopyStampedByAnOlderReleaseIsNotGrownOnTheQuickPath(t *testing.T) {
	v := t.TempDir()
	tok := "ghp_" + strings.Repeat("a1B2c3", 6)
	tokLine := `{"type":"user","sessionId":"s1","message":{"role":"user","content":"GITHUB_TOKEN=` + tok + `"}}` + "\n"
	src := filepath.Join(t.TempDir(), "s1.jsonl")
	if err := os.WriteFile(src, []byte(`{"type":"user","sessionId":"s1","message":{"role":"user","content":"hi"}}`+"\n"+tokLine), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Candidate{Harness: "claude-code", SessionID: "s1", Source: src, Project: "kestrel"}
	was := redactRules
	t.Cleanup(func() { redactRules = was })

	redactRules = "v0.5.8"
	first := archiveTranscript(v, c, true)
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	// What v0.5.8 kept, had it not known the shape: the line as the host wrote it.
	dst := filepath.Join(v, filepath.FromSlash(first.Path))
	held, _ := os.ReadFile(dst)
	lines := strings.SplitAfter(string(held), "\n")
	lines[1] = tokLine
	older := strings.Join(lines, "")
	if err := os.WriteFile(dst, []byte(older), 0o600); err != nil {
		t.Fatal(err)
	}
	stampRedacted(v, first.Path, int64(len(older)))

	redactRules = "v0.5.10"
	f, _ := os.OpenFile(src, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"user","sessionId":"s1","message":{"role":"user","content":"more"}}` + "\n")
	f.Close()
	if a := archiveTranscript(v, c, true); a.Err != nil {
		t.Fatal(a.Err)
	}
	if got, _ := os.ReadFile(dst); strings.Contains(string(got), tok) {
		t.Fatal("the quick path grew a copy an older release left a token in")
	}
	if !stampHolds(v, first.Path, fileSize(t, dst)) {
		t.Error("the full pass did not stamp the copy it made")
	}
}

func fileSize(t *testing.T, p string) int64 {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}
