package transcript

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// A3 — everything that is not Claude Code or Codex.
//
// Two ways in, neither of which requires a bespoke Go reader:
//
//   - txcript's "Simple interchange JSON" piped on stdin. An agent Logos has
//     never heard of can hand us a session with no adapter at all.
//   - the txcript binary on PATH, shelled out to for its 16 native formats.
//     Optional, never required (the plan is explicit): Logos stays a single Go
//     binary with no toolchain dependency, and A1/A2 already cover the formats
//     this repository's own users run.

// interchange is txcript's documented Simple interchange JSON. Kept permissive:
// unknown fields are ignored and a missing role defaults to user, so a slightly
// different producer still round-trips.
type interchange struct {
	Harness   string `json:"harness"`
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Project   string `json:"project"`
	Cwd       string `json:"cwd"`
	Started   int64  `json:"started"`
	Ended     int64  `json:"ended"`
	Messages  []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Text    string `json:"text"`
		Name    string `json:"name"`
		Tool    string `json:"tool"`
		Status  string `json:"status"`
	} `json:"messages"`
}

// ReadInterchange parses txcript Simple interchange JSON from r into a Session.
// No file is touched; the source pointer is whatever the caller records.
func ReadInterchange(r io.Reader) (*Session, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 64*1024*1024))
	if err != nil {
		return nil, err
	}
	var ic interchange
	if err := json.Unmarshal(raw, &ic); err != nil {
		return nil, fmt.Errorf("stdin is not txcript interchange JSON: %w", err)
	}
	if len(ic.Messages) == 0 {
		return nil, fmt.Errorf("interchange JSON has no messages")
	}

	s := &Session{
		Harness: firstNonEmpty(ic.Harness, "interchange"),
		ID:      firstNonEmpty(ic.ID, ic.SessionID),
		Path:    "(stdin)",
		Project: firstNonEmpty(ic.Project, projectFromDir(ic.Cwd)),
		Started: ic.Started,
		Ended:   ic.Ended,
	}
	sum := hashBytes(raw)
	s.Hash = sum
	if s.ID == "" {
		s.ID = sum[:12]
	}
	for _, m := range ic.Messages {
		text := firstNonEmpty(m.Content, m.Text)
		role := m.Role
		switch role {
		case "assistant":
			s.Turns = append(s.Turns, Turn{Role: "assistant", Text: strings.TrimSpace(text)})
		case "tool", "tool_result", "function_call_output":
			s.Turns = append(s.Turns, Turn{
				Role:   "tool",
				Tool:   firstNonEmpty(m.Tool, m.Name),
				Text:   strings.TrimSpace(text),
				Status: normStatus(m.Status),
			})
		default:
			s.Turns = append(s.Turns, Turn{Role: "user", Text: strings.TrimSpace(text)})
		}
	}
	return s, nil
}

func normStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ok", "success", "completed":
		return "ok"
	case "error", "failed", "failure":
		return "error"
	default:
		return ""
	}
}

// txcriptReader adapts one of txcript's native formats. It is a registry entry
// like any other so A3 is not a special case; it just reports "install txcript"
// as its skip reason until the binary is on PATH.
type txcriptReader struct {
	name   string // harness name, as both Logos and txcript spell it
	format string // txcript --format value
}

func (t txcriptReader) harness() string { return t.name }

func (t txcriptReader) root() string {
	// txcript owns discovery for these formats. Without the binary there is
	// nothing this reader can do, and Available() turns an empty root into the
	// "install txcript" reason via reasonNoRoot.
	if !txcriptOnPath() {
		return ""
	}
	// With txcript present but no standard on-disk location Logos knows, the
	// reader still can't enumerate — a caller passes an explicit path to
	// `brain ingest --harness <name> <path>`. Report available-but-empty.
	return txcriptSentinel
}

// txcriptSentinel is a non-path marker: root() must be non-empty for the
// harness to count as "found", but there is no directory to name.
const txcriptSentinel = "(txcript)"

func (t txcriptReader) discover() ([]string, error) {
	// No standard location to walk; discovery for these formats is explicit.
	return nil, nil
}

func (t txcriptReader) read(path string) (*Session, error) {
	if !txcriptOnPath() {
		return nil, &SkipReason{Harness: t.name, Why: "txcript is not on PATH; install it for this format"}
	}
	out, err := exec.Command("txcript", "export", "--format", "json", path).Output()
	if err != nil {
		return nil, fmt.Errorf("txcript export %s: %w", path, err)
	}
	s, err := ReadInterchange(strings.NewReader(string(out)))
	if err != nil {
		return nil, err
	}
	s.Harness = t.name
	s.Path = mustAbs(path)
	if h, herr := hashFile(path); herr == nil {
		s.Hash = h
	}
	return s, nil
}

func txcriptOnPath() bool {
	_, err := exec.LookPath("txcript")
	return err == nil
}

// registerTxcriptReaders adds the formats txcript covers that matter to this
// project's users. The list is deliberately short — it is not a claim to
// support all 16, only the ones worth naming in `brain doctor`.
func registerTxcriptReaders() {
	for _, f := range []txcriptReader{
		{name: "cursor", format: "cursor"},
		{name: "aider", format: "aider"},
		{name: "cline", format: "cline"},
		{name: "windsurf", format: "windsurf"},
	} {
		if _, taken := registry[f.name]; !taken {
			register(f)
		}
	}
}
