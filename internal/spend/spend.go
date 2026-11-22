// Package spend reads how large a context a coding agent's session reached,
// from the transcripts the hosts already keep: Claude Code's under
// ~/.claude/projects and Codex's under ~/.codex/sessions. It is the one
// figure on `logos stats --usage` that is not Logos's own ledger, and it is
// labelled so: what the host recorded, not what anyone was billed.
//
// It reads and never writes. The transcripts are the host's files, and the
// figures are recomputed on every run, so there is no cache to fall out of
// step with them and nothing for an index rebuild to lose.
package spend

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/transcript"
)

// Sample is the context one request of a session read: everything the model
// was given on it, as the host recorded.
type Sample struct {
	TS      int64
	Context int64
}

// PeakAt is the largest context in samples at or before cutoff, 0 when none
// is. A restore continues the session as the checkpoint left it, so context
// the session grew afterwards was never part of what the pack stood in for.
func PeakAt(samples []Sample, cutoff int64) int64 {
	var peak int64
	for _, s := range samples {
		if s.TS <= cutoff && s.Context > peak {
			peak = s.Context
		}
	}
	return peak
}

// Samples is, per host session id, the context of every request that session
// made, oldest first; a session with no transcript on this machine is absent.
// A restore continues a session that began before any dashboard window, so
// lines are not cut at a start time — only files last written before
// writtenSince are left unopened, since a session cannot have ended before its
// file was last touched. A host whose directory is missing is skipped.
func Samples(writtenSince time.Time, ids map[string]bool) (map[string][]Sample, error) {
	out := map[string][]Sample{}
	if len(ids) == 0 {
		return out, nil
	}
	if root := transcript.ClaudeProjectsRoot(); root != "" {
		if err := readClaude(out, root, writtenSince, ids); err != nil {
			return nil, err
		}
	}
	if root := transcript.CodexSessionsRoot(); root != "" {
		if err := readCodex(out, root, writtenSince, ids); err != nil {
			return nil, err
		}
	}
	for _, s := range out {
		sort.Slice(s, func(i, j int) bool { return s[i].TS < s[j].TS })
	}
	return out, nil
}

// transcripts lists the .jsonl files under root changed since from. A file
// last written before then cannot hold a line of the sessions asked for, and
// skipping it unread keeps a lookup off the whole history's bytes.
func transcripts(root string, from time.Time) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // an unreadable subdirectory is the host's, not ours to fail on
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().Before(from) {
			return nil
		}
		out = append(out, p)
		return nil
	})
	return out, err
}

func scanLines(path string, each func(n int, b []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// A transcript line carries whole tool outputs and can run to megabytes.
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for n := 1; sc.Scan(); n++ {
		each(n, sc.Bytes())
	}
	return sc.Err()
}

// parseTS reports ok=false for a missing or malformed timestamp: read as zero
// it would fall before every cutoff and the line vanish with no trace.
func parseTS(s string) (int64, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}

type claudeLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input         int64 `json:"input_tokens"`
			CacheCreation int64 `json:"cache_creation_input_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// claudeSessionID is the id Claude Code records in a checkpoint's host_session:
// the line's sessionId, or, when a line carries none, its file's stem, which
// Claude Code names after the session.
func claudeSessionID(id, path string) string {
	if id != "" {
		return id
	}
	return strings.TrimSuffix(filepath.Base(path), ".jsonl")
}

// rawSessionID reads a line's sessionId without parsing the line.
func rawSessionID(b []byte) (string, bool) {
	const key = `"sessionId":"`
	i := bytes.Index(b, []byte(key))
	if i < 0 {
		return "", false
	}
	rest := b[i+len(key):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return "", false
	}
	return string(rest[:j]), true
}

// codexSessionID pulls the uuid tail out of rollout-<ts>-<uuid>.jsonl, the
// fallback for a file whose session_meta line has not been read.
func codexSessionID(path string) string {
	name := strings.TrimPrefix(strings.TrimSuffix(filepath.Base(path), ".jsonl"), "rollout-")
	if parts := strings.Split(name, "-"); len(parts) >= 5 {
		return strings.Join(parts[len(parts)-5:], "-")
	}
	return name
}

// readClaude counts each API request once. Claude Code writes one transcript
// line per content block of a response, each repeating the request's usage,
// and a resumed session copies earlier lines into a new file. The message id
// and request id together name the request, wherever it was written.
func readClaude(out map[string][]Sample, root string, since time.Time, ids map[string]bool) error {
	files, err := transcripts(root, since)
	if err != nil {
		return err
	}
	type req struct {
		session string
		s       Sample
	}
	reqs := map[string]req{}
	for _, path := range files {
		_ = scanLines(path, func(n int, b []byte) {
			if !bytes.Contains(b, []byte(`"usage"`)) || !bytes.Contains(b, []byte(`"assistant"`)) {
				return
			}
			// Most lines belong to other sessions; reading the id off the raw
			// line first spares parsing them. A line with no id at all falls
			// through to the full parse.
			if id, ok := rawSessionID(b); ok && id != "" && !ids[id] {
				return
			}
			var ln claudeLine
			if json.Unmarshal(b, &ln) != nil {
				return
			}
			m := ln.Message
			if ln.Type != "assistant" || m.Usage == nil || m.Model == "" || strings.HasPrefix(m.Model, "<") {
				return // "<synthetic>" is Claude Code's own placeholder, not a request
			}
			ts, ok := parseTS(ln.Timestamp)
			if !ok {
				return
			}
			id := claudeSessionID(ln.SessionID, path)
			if !ids[id] {
				return
			}
			// With no message id, two requests in one second of one file
			// would share a key and the second replace the first; the line
			// number keeps them apart.
			key := m.ID + "|" + ln.RequestID
			if m.ID == "" {
				key = fmt.Sprintf("%s|%d", path, n)
			}
			reqs[key] = req{id, Sample{ts, m.Usage.Input + m.Usage.CacheCreation + m.Usage.CacheRead}}
		})
	}
	for _, q := range reqs {
		out[q.session] = append(out[q.session], q.s)
	}
	return nil
}

type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Info *struct {
			Total struct {
				Input  int64 `json:"input_tokens"`
				Output int64 `json:"output_tokens"`
			} `json:"total_token_usage"`
			Last struct {
				Input int64 `json:"input_tokens"`
			} `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

// readCodex reads one sample per token_count event whose running total moved.
// Codex repeats a token_count when only its rate limits changed, which would
// otherwise be a second sample of the same request.
func readCodex(out map[string][]Sample, root string, since time.Time, ids map[string]bool) error {
	files, err := transcripts(root, since)
	if err != nil {
		return err
	}
	for _, path := range files {
		id := codexSessionID(path)
		var prevIn, prevOut int64
		_ = scanLines(path, func(_ int, b []byte) {
			if !bytes.Contains(b, []byte(`"session_meta"`)) && !bytes.Contains(b, []byte(`"token_count"`)) {
				return
			}
			var ln codexLine
			if json.Unmarshal(b, &ln) != nil {
				return
			}
			switch {
			case ln.Type == "session_meta":
				if ln.Payload.ID != "" {
					id = ln.Payload.ID
				}
			case ln.Payload.Type == "token_count" && ln.Payload.Info != nil:
				t := ln.Payload.Info.Total
				moved := t.Input-prevIn+t.Output-prevOut > 0
				prevIn, prevOut = t.Input, t.Output
				ts, ok := parseTS(ln.Timestamp)
				if !moved || !ok || !ids[id] {
					return
				}
				out[id] = append(out[id], Sample{ts, ln.Payload.Info.Last.Input})
			}
		})
	}
	return nil
}
