package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Codex writes rollout-<timestamp>-<uuid>.jsonl files under
// ~/.codex/sessions/YYYY/MM/DD/. One JSON object per line, wrapped in an
// envelope: a top-level "type" and "payload", plus a top-level RFC3339
// "timestamp". The conversation lives in type:"response_item" lines; the
// type:"event_msg" lines are cosmetic duplicates and are skipped.

// BrainCodexSessionsEnv overrides the Codex sessions directory for tests.
const BrainCodexSessionsEnv = "BRAIN_CODEX_SESSIONS"

type codexReader struct{}

func (codexReader) harness() string { return "codex" }

func (codexReader) root() string {
	if v := os.Getenv(BrainCodexSessionsEnv); v != "" {
		if dirExists(v) {
			return v
		}
		return ""
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(h, ".codex", "sessions")
	if dirExists(p) {
		return p
	}
	return ""
}

func (r codexReader) discover() ([]string, error) {
	root := r.root()
	if root == "" {
		return nil, nil
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subdirectory is skipped, not fatal
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortByMtimeDesc(out)
	return out, nil
}

type codexLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type codexPayload struct {
	Type string `json:"type"`

	// session_meta
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`

	// message
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`

	// function_call / custom_tool_call
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Input     json.RawMessage `json:"input"`
	Status    string          `json:"status"`
	CallID    string          `json:"call_id"`

	// function_call_output / custom_tool_call_output
	Output json.RawMessage `json:"output"`
}

type codexContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (codexReader) read(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	hash, err := hashFile(path)
	if err != nil {
		return nil, err
	}

	s := &Session{
		Harness: "codex",
		Path:    mustAbs(path),
		ID:      codexIDFromName(filepath.Base(path)),
		Hash:    hash,
	}

	toolName := map[string]string{}  // call_id -> tool name
	toolInput := map[string]string{} // call_id -> invocation

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var ln codexLine
		if err := json.Unmarshal([]byte(raw), &ln); err != nil {
			s.Skipped++
			continue
		}
		if ln.Type != "session_meta" && ln.Type != "response_item" {
			continue // event_msg, turn_context, world_state: not the conversation
		}
		var p codexPayload
		if err := json.Unmarshal(ln.Payload, &p); err != nil {
			s.Skipped++
			continue
		}

		if ts := parseRFC3339(ln.Timestamp); ts > 0 {
			if s.Started == 0 || ts < s.Started {
				s.Started = ts
			}
			if ts > s.Ended {
				s.Ended = ts
			}
		}

		if ln.Type == "session_meta" {
			if s.ID == "" {
				s.ID = firstNonEmpty(p.ID, p.SessionID)
			}
			if p.Cwd != "" {
				s.Project = projectFromDir(p.Cwd)
			}
			continue
		}

		switch p.Type {
		case "message":
			if p.Role != "user" && p.Role != "assistant" {
				continue // developer/system: harness boilerplate, not the session
			}
			if t := codexText(p.Content); t != "" {
				s.Turns = append(s.Turns, Turn{Role: p.Role, Text: t})
			}
		case "function_call", "custom_tool_call":
			if p.CallID != "" && p.Name != "" {
				toolName[p.CallID] = p.Name
				toolInput[p.CallID] = codexInvocation(p.Arguments, p.Input)
			}
		case "function_call_output", "custom_tool_call_output":
			s.Turns = append(s.Turns, Turn{
				Role:   "tool",
				Tool:   toolName[p.CallID],
				Text:   codexText(p.Output),
				Status: codexStatus(p.Status, p.Output),
				Input:  toolInput[p.CallID],
			})
		case "reasoning":
			// Internal reasoning is not an observed fact; a distiller must not
			// cite it as evidence.
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(s.Turns) == 0 && s.Skipped == 0 {
		return nil, fmt.Errorf("%s: no conversation turns found", path)
	}
	return s, nil
}

// codexText renders a message content or tool output, which is any of: a plain
// JSON string, a JSON string that itself holds a JSON array of content items,
// or a JSON array of content items.
func codexText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		s := strings.TrimSpace(str)
		if strings.HasPrefix(s, "[") {
			if inner := codexItems([]byte(s)); inner != "" {
				return inner
			}
		}
		return s
	}
	return codexItems(raw)
}

func codexItems(raw json.RawMessage) string {
	var items []codexContentItem
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var parts []string
	for _, it := range items {
		if t := strings.TrimSpace(it.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

var codexExitRe = regexp.MustCompile(`(?i)exited with code (\d+)`)

// codexStatus reads whether a tool call succeeded. The custom_tool_call status
// field is authoritative when present; otherwise the classic exec output
// carries "exited with code N".
func codexStatus(status string, output json.RawMessage) string {
	switch strings.ToLower(status) {
	case "completed", "success":
		return "ok"
	case "failed", "error", "incomplete":
		return "error"
	}
	if m := codexExitRe.FindStringSubmatch(string(output)); m != nil {
		if m[1] == "0" {
			return "ok"
		}
		return "error"
	}
	return ""
}

// codexIDFromName pulls the uuid tail out of rollout-<ts>-<uuid>.jsonl. The
// session_meta line's id is preferred when present; this is the fallback so a
// truncated file with no meta still has a stable id.
func codexIDFromName(name string) string {
	name = strings.TrimSuffix(name, ".jsonl")
	name = strings.TrimPrefix(name, "rollout-")
	// The uuid is the last five dash-separated groups.
	parts := strings.Split(name, "-")
	if len(parts) >= 5 {
		return strings.Join(parts[len(parts)-5:], "-")
	}
	return name
}

// codexInvocation extracts the command from a tool call. function_call carries
// a JSON "arguments" string ({"command":[...]} or {"command":"..."});
// custom_tool_call carries a bare "input" that is often the command itself.
func codexInvocation(arguments string, input json.RawMessage) string {
	if s := strings.TrimSpace(arguments); s != "" {
		var m map[string]any
		if json.Unmarshal([]byte(s), &m) == nil {
			switch v := m["command"].(type) {
			case string:
				return strings.TrimSpace(v)
			case []any:
				parts := make([]string, 0, len(v))
				for _, e := range v {
					if es, ok := e.(string); ok {
						parts = append(parts, es)
					}
				}
				return strings.TrimSpace(strings.Join(parts, " "))
			}
			for _, k := range []string{"cmd", "file_path", "path"} {
				if sv, ok := m[k].(string); ok {
					return strings.TrimSpace(sv)
				}
			}
		}
		return s
	}
	if t := codexText(input); t != "" {
		return t
	}
	return ""
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
