package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// Receipt is one checkpoint save as the host saw it happen: which of its
// conversations called Logos, and when the answer came back.
type Receipt struct {
	HostSession string
	At          time.Time
}

// receiptLine is the first line of every checkpoint receipt Logos has ever
// returned, once the optional "✓ Logos · " marker is off (LOGOS_ANNOUNCE=off
// drops it): "checkpoint saved to logos|brain — sessions/….md", "Checkpoint
// written to sessions/….md in the vault.", the "already saved" of an
// identical retry, and the CLI's "checkpoint written: sessions/….md". It is
// anchored at the start of the line, unlike ingest's handoffReceipt, because
// here the same words later in a line are someone quoting a receipt.
var receiptLine = regexp.MustCompile(`(?i)^checkpoint (?:already )?(?:saved|written)\b[^\x60]*?\x60?sessions/([^\s\x60]+\.md)`)

// CheckpointReceipts maps "<scope>/<stem>" to every receipt for that
// checkpoint in the Claude Code and Codex transcripts on this machine.
//
// It exists for checkpoints written before host_session was recorded: the
// frontmatter cannot say which conversation wrote them, but that
// conversation's transcript still holds Logos's answer naming the file. Only
// the answer itself counts — the first line of a Logos tool result. The same
// text in a grep's output, a shell command or an agent's reply is someone
// quoting a receipt, usually from another conversation, and would file the
// checkpoint under the conversation that happened to read it.
//
// The folder part of the path is dropped from the key, because a checkpoint's
// folder is exactly what is being decided; scope and stem name it on their
// own, since claimCheckpoint keeps a stem unique within a scope.
//
// A transcript that cannot be read is passed over and its path returned in
// unreadable, not made fatal: one file locked by a running host, or left
// unreadable by a permissions slip, would otherwise stop every other
// checkpoint from finding its conversation. The caller reports the paths, so
// a checkpoint that stays unattributed because of one is not silent.
func CheckpointReceipts() (out map[string][]Receipt, unreadable []string, err error) {
	out = map[string][]Receipt{}
	add := func(text, host, ts string) {
		key := receiptKey(text)
		at, err := time.Parse(time.RFC3339Nano, ts)
		if key == "" || host == "" || err != nil {
			return
		}
		out[key] = append(out[key], Receipt{HostSession: host, At: at})
	}
	cc, err := claudeCodeReader{}.discover()
	if err != nil {
		return nil, nil, err
	}
	for _, path := range cc {
		if err := eachReceiptLine(path, func(line []byte) {
			var l claudeLine
			var m claudeMessage
			if json.Unmarshal(line, &l) != nil || l.Type != "user" || json.Unmarshal(l.Message, &m) != nil {
				return
			}
			var blocks []claudeBlock
			if json.Unmarshal(m.Content, &blocks) != nil {
				return
			}
			for _, b := range blocks {
				if b.Type == "tool_result" {
					add(flattenContent(b.Content), l.SessionID, l.Timestamp)
				}
			}
		}); err != nil {
			unreadable = append(unreadable, path)
		}
	}
	cx, err := codexReader{}.discover()
	if err != nil {
		return nil, nil, err
	}
	for _, path := range cx {
		if err := eachReceiptLine(path, func(line []byte) {
			var l struct {
				Timestamp string `json:"timestamp"`
				Type      string `json:"type"`
				Payload   struct {
					Type     string `json:"type"`
					ThreadID string `json:"thread_id"`
					Item     struct {
						Type   string `json:"type"`
						Result struct {
							Content []codexContentItem `json:"content"`
						} `json:"result"`
					} `json:"item"`
				} `json:"payload"`
			}
			// Codex records an MCP call twice, as an event and as a response
			// item; the event alone carries the thread id, so it is the one read.
			if json.Unmarshal(line, &l) != nil || l.Type != "event_msg" || l.Payload.Type != "item_completed" || l.Payload.Item.Type != "McpToolCall" {
				return
			}
			if c := l.Payload.Item.Result.Content; len(c) > 0 {
				add(c[0].Text, l.Payload.ThreadID, l.Timestamp)
			}
		}); err != nil {
			unreadable = append(unreadable, path)
		}
	}
	return out, unreadable, nil
}

// receiptKey is "<scope>/<stem>" for a tool result whose first line is a
// checkpoint receipt, or "" for anything else.
func receiptKey(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	m := receiptLine.FindStringSubmatch(strings.TrimPrefix(first, "✓ Logos · "))
	if m == nil {
		return ""
	}
	rel := strings.TrimSuffix(m[1], ".md")
	i := strings.LastIndex(rel, "/")
	if i <= 0 {
		return ""
	}
	scope, stem := rel[:i], rel[i+1:]
	if j := strings.Index(scope, "/_convos/"); j >= 0 {
		scope = scope[:j]
	}
	return scope + "/" + stem
}

// eachReceiptLine hands fn each line that mentions a receipt at all. The
// substring test runs before any JSON is decoded: a machine's transcripts
// run to gigabytes, and nearly every line is something else.
func eachReceiptLine(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // removed between discovery and here
		}
		return err
	}
	defer f.Close()
	// Lower-case "heckpoint" catches "Checkpoint" and "checkpoint" alike.
	needles := [][]byte{[]byte("heckpoint saved"), []byte("heckpoint written"), []byte("heckpoint already saved")}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && containsAny(line, needles) {
			fn(line)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func containsAny(line []byte, needles [][]byte) bool {
	for _, n := range needles {
		if bytes.Contains(line, n) {
			return true
		}
	}
	return false
}
