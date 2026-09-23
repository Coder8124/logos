package transcript

import (
	"bytes"
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
	Timestamp string `json:"timestamp"`
	Ended     int64  `json:"ended"`
	Messages  []struct {
		Role string `json:"role"`
		// Content is a string at txcript's L0 and an array of Anthropic-style
		// blocks from L1 on — which is every session with a tool call in it,
		// so every real export. Typed as a string, the whole document failed
		// to decode the moment one block appeared (#172).
		Content json.RawMessage `json:"content"`
		Text    string          `json:"text"`
		Name    string          `json:"name"`
		Tool    string          `json:"tool"`
		Status  string          `json:"status"`
		Input   string          `json:"input"`
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
	if s.Started == 0 {
		s.Started = parseRFC3339(ic.Timestamp)
	}
	sum := hashBytes(raw)
	s.Hash = sum
	if s.ID == "" {
		s.ID = sum[:12]
	}
	var calls toolCalls
	for _, m := range ic.Messages {
		// Only an array is blocks. JSON null unmarshals into a slice without
		// error, and taking this path for it dropped the message's text and
		// tool fields — everything a null-content message had to say.
		var blocks []claudeBlock
		if bytes.HasPrefix(bytes.TrimSpace(m.Content), []byte("[")) && json.Unmarshal(m.Content, &blocks) == nil {
			s.Turns = append(s.Turns, calls.turns(strings.ToLower(m.Role), blocks)...)
			continue
		}
		var content string
		json.Unmarshal(m.Content, &content)
		text := firstNonEmpty(content, m.Text)
		switch strings.ToLower(m.Role) {
		case "assistant":
			s.Turns = append(s.Turns, Turn{Role: "assistant", Text: strings.TrimSpace(text)})
		case "tool", "tool_result", "function_call_output":
			s.Turns = append(s.Turns, Turn{
				Role:   "tool",
				Tool:   firstNonEmpty(m.Tool, m.Name),
				Text:   strings.TrimSpace(text),
				Status: normStatus(m.Status),
				Input:  strings.TrimSpace(m.Input),
			})
		default:
			s.Turns = append(s.Turns, Turn{Role: "user", Text: strings.TrimSpace(text)})
		}
	}
	return s, nil
}

// toolCalls pairs Simple's tool_result blocks with the tool_use they answer.
// Simple lets both ids be left out, and then a result pairs with the oldest
// call not yet answered — txcript's own rule, so a document that pairs under
// txcript pairs the same way here. Without it an id-less result became a tool
// turn with no tool name and no command, which is the half a harvest reads.
type toolCalls struct {
	pending []pendingCall
}

type pendingCall struct {
	id, name, input string
}

// turns maps one message's blocks the way the Claude Code reader maps its own
// (Simple is Anthropic's block convention): text is a turn, a tool_result is a
// tool turn carrying the call's name and invocation, thinking is left out.
func (c *toolCalls) turns(role string, blocks []claudeBlock) []Turn {
	var out []Turn
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				out = append(out, Turn{Role: normRole(role), Text: t})
			}
		case "tool_use":
			c.pending = append(c.pending, pendingCall{b.ID, b.Name, toolInvocation(b.Name, b.Input)})
		case "tool_result":
			call := c.answer(b.ToolUseID)
			out = append(out, Turn{
				Role:   "tool",
				Tool:   call.name,
				Text:   flattenResult(b.Content),
				Status: okOrError(b.IsError),
				Input:  call.input,
			})
		case "thinking", "redacted_thinking":
			// Not part of what happened; a distiller must not treat internal
			// reasoning as an observed fact.
		}
	}
	return out
}

// answer removes and returns the call a result belongs to: the one with its id
// when it names one that is pending, otherwise the oldest pending call.
func (c *toolCalls) answer(id string) pendingCall {
	i := -1
	if id != "" {
		for j, p := range c.pending {
			if p.id == id {
				i = j
				break
			}
		}
	}
	if i < 0 {
		for j, p := range c.pending {
			// An id-less result takes the oldest call; one naming an id nobody
			// issued takes the oldest id-less call rather than stealing another
			// result's partner.
			if id == "" || p.id == "" {
				i = j
				break
			}
		}
	}
	if i < 0 {
		return pendingCall{}
	}
	call := c.pending[i]
	c.pending = append(c.pending[:i], c.pending[i+1:]...)
	return call
}

// flattenResult is flattenContent plus Simple's third shape: a tool_result's
// content may be any JSON, and dropping it would leave a tool turn that says
// nothing about what the tool returned.
func flattenResult(raw json.RawMessage) string {
	if t := flattenContent(raw); t != "" || len(raw) == 0 || string(raw) == "null" {
		return t
	}
	return strings.TrimSpace(string(raw))
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
	name string // harness name as Logos spells it
	from string // txcript's harness id, passed to export --from
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
	// reader still can't enumerate — a caller names one session by its txcript
	// id, `logos ingest --harness <name> --path <id>`. Report available-but-empty.
	return txcriptSentinel
}

// txcriptSentinel is a non-path marker: root() must be non-empty for the
// harness to count as "found", but there is no directory to name.
const txcriptSentinel = "(txcript)"

func (t txcriptReader) discover() ([]string, error) {
	// No standard location to walk; discovery for these formats is explicit.
	return nil, nil
}

// read takes a txcript session id, not a path: txcript owns discovery for these
// formats, and `txcript export` finds a session by id (or any unambiguous
// prefix, or its title) and writes it as a Simple document. It has no --format
// flag and does not take a path; the call this replaced passed both, and so
// failed for every session it was ever handed (#172).
func (t txcriptReader) read(id string) (*Session, error) {
	if !txcriptOnPath() {
		return nil, &SkipReason{Harness: t.name, Why: "txcript is not on PATH; install it for this format"}
	}
	var stderr strings.Builder
	cmd := exec.Command("txcript", "export", id, "--from", t.from)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("txcript export %s --from %s: %w: %s", id, t.from, err, msg)
		}
		return nil, fmt.Errorf("txcript export %s --from %s: %w", id, t.from, err)
	}
	s, err := ReadInterchange(strings.NewReader(string(out)))
	if err != nil {
		return nil, err
	}
	s.Harness = t.name
	// Where to find it again, in the terms the source understands. The export's
	// content hash, set by ReadInterchange, stays the change detector.
	s.Path = "txcript:" + t.from + ":" + id
	return s, nil
}

func txcriptOnPath() bool {
	_, err := exec.LookPath("txcript")
	return err == nil
}

// registerTxcriptReaders adds the formats txcript covers that Logos does not
// read natively. The list is deliberately short — it is not a claim to support
// all 16, only the ones worth naming in `logos doctor` — and every name on it
// is one txcript actually reads. It used to name aider, cline and windsurf,
// none of which txcript has ever supported (#172).
func registerTxcriptReaders() {
	for _, f := range []txcriptReader{
		{name: "opencode", from: "opencode"},
		{name: "amp", from: "amp"},
		{name: "grok", from: "grok"},
	} {
		if _, taken := registry[f.name]; !taken {
			register(f)
		}
	}
}
