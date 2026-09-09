package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Claude Code writes one JSON object per line to
// ~/.claude/projects/<slugged-cwd>/<session-uuid>.jsonl. Most line types are
// UI bookkeeping (mode, permission-mode, ai-title, file-history-snapshot, ...);
// only "user" and "assistant" lines carry the conversation. A native reader
// beats shelling out to txcript for the format this repository's own users run
// most.

// BrainClaudeProjectsEnv overrides the Claude Code projects directory, so a
// test points at a fixture tree instead of the real one. Same pattern as
// BRAIN_VAULT.
const BrainClaudeProjectsEnv = "BRAIN_CLAUDE_PROJECTS"

type claudeCodeReader struct{}

func (claudeCodeReader) harness() string { return "claude-code" }

func (claudeCodeReader) root() string {
	if v := os.Getenv(BrainClaudeProjectsEnv); v != "" {
		if dirExists(v) {
			return v
		}
		return ""
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(h, ".claude", "projects")
	if dirExists(p) {
		return p
	}
	return ""
}

func (r claudeCodeReader) discover() ([]string, error) {
	root := r.root()
	if root == "" {
		return nil, nil
	}
	var out []string
	// One level of project directories, each holding session .jsonl files.
	projects, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, p.Name()))
		if err != nil {
			continue // a project dir we cannot read is skipped, not fatal
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			out = append(out, filepath.Join(root, p.Name(), f.Name()))
		}
	}
	sortByMtimeDesc(out)
	return out, nil
}

// claudeLine is the subset of a Claude Code transcript line this reader needs.
type claudeLine struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Cwd       string          `json:"cwd"`
	Slug      string          `json:"slug"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`        // tool_use
	ID        string          `json:"id"`          // tool_use
	ToolUseID string          `json:"tool_use_id"` // tool_result
	IsError   bool            `json:"is_error"`    // tool_result
	Content   json.RawMessage `json:"content"`     // tool_result: string or []block
	Input     json.RawMessage `json:"input"`       // tool_use
}

func (claudeCodeReader) read(path string) (*Session, error) {
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
		Harness: "claude-code",
		Path:    mustAbs(path),
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Hash:    hash,
	}
	// The directory slug is the recorded cwd with separators swapped; its
	// basename is the project. Prefer it over reading cwd out of the content,
	// per the plan.
	s.Project = projectFromSlug(filepath.Base(filepath.Dir(path)))

	toolName := map[string]string{}  // tool_use id -> tool name
	toolInput := map[string]string{} // tool_use id -> invocation (command, path)

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var ln claudeLine
		if err := json.Unmarshal([]byte(raw), &ln); err != nil {
			s.Skipped++ // named in the count, not fatal (invariant 3)
			continue
		}
		if ln.Type != "user" && ln.Type != "assistant" {
			continue // UI bookkeeping line
		}
		if s.ID == "" && ln.SessionID != "" {
			s.ID = ln.SessionID
		}
		if s.Project == "" && ln.Cwd != "" {
			s.Project = projectFromDir(ln.Cwd)
		}
		if ts := parseRFC3339(ln.Timestamp); ts > 0 {
			if s.Started == 0 || ts < s.Started {
				s.Started = ts
			}
			if ts > s.Ended {
				s.Ended = ts
			}
		}

		var msg claudeMessage
		if err := json.Unmarshal(ln.Message, &msg); err != nil {
			s.Skipped++
			continue
		}
		turns, malformed := claudeTurns(ln.Type, msg, toolName, toolInput)
		s.Skipped += malformed
		s.Turns = append(s.Turns, turns...)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(s.Turns) == 0 && s.Skipped == 0 {
		return nil, fmt.Errorf("%s: no user or assistant turns found", path)
	}
	return s, nil
}

// claudeTurns turns one message into zero or more Turns. content may be a bare
// string or an array of typed blocks.
func claudeTurns(lineType string, msg claudeMessage, toolName, toolInput map[string]string) (turns []Turn, malformed int) {
	role := msg.Role
	if role == "" {
		role = lineType
	}

	// The simple case: content is just a string.
	var str string
	if json.Unmarshal(msg.Content, &str) == nil {
		if t := strings.TrimSpace(str); t != "" {
			turns = append(turns, Turn{Role: normRole(role), Text: t})
		}
		return turns, 0
	}

	var blocks []claudeBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return nil, 1
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				turns = append(turns, Turn{Role: normRole(role), Text: t})
			}
		case "tool_use":
			if b.ID != "" && b.Name != "" {
				toolName[b.ID] = b.Name
				toolInput[b.ID] = toolInvocation(b.Name, b.Input)
			}
		case "tool_result":
			turns = append(turns, Turn{
				Role:   "tool",
				Tool:   toolName[b.ToolUseID],
				Text:   flattenContent(b.Content),
				Status: okOrError(b.IsError),
				Input:  toolInput[b.ToolUseID],
			})
		case "thinking", "redacted_thinking":
			// Not part of what happened; a distiller must not treat internal
			// reasoning as an observed fact.
		}
	}
	return turns, 0
}

// toolInvocation pulls the one field of a tool's input that a harvest can use:
// the command for a shell tool, the path for a file tool. Everything else is
// left out — a harvest reports what ran and what was edited, not full argument
// blobs, which is where pasted secrets would ride along.
func toolInvocation(tool string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	for _, key := range []string{"command", "cmd", "file_path", "path", "notebook_path"} {
		if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func normRole(r string) string {
	switch r {
	case "user", "assistant":
		return r
	default:
		return "user"
	}
}

func okOrError(isErr bool) string {
	if isErr {
		return "error"
	}
	return "ok"
}

// flattenContent renders a tool_result's content, which is a string or an
// array of {type:"text", text:...} blocks.
func flattenContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return strings.TrimSpace(str)
	}
	var blocks []claudeBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

// projectFromSlug reverses a Claude Code project directory name back to a
// project. The slug is a full path with every separator replaced by "-", so it
// cannot be reversed exactly (real path segments contain "-" too) — but the
// last segment is the working directory's basename, which is all we need.
func projectFromSlug(slug string) string {
	slug = strings.TrimPrefix(slug, "-")
	if slug == "" {
		return ""
	}
	parts := strings.Split(slug, "-")
	return parts[len(parts)-1]
}

func parseRFC3339(s string) int64 {
	if s == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return 0
		}
	}
	return t.Unix()
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func mustAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func sortByMtimeDesc(paths []string) {
	mtime := func(p string) int64 {
		fi, err := os.Stat(p)
		if err != nil {
			return 0
		}
		return fi.ModTime().UnixNano()
	}
	sort.Slice(paths, func(i, j int) bool { return mtime(paths[i]) > mtime(paths[j]) })
}
