// Package spend reads what coding agents on this machine spent, from the
// transcripts their hosts already keep: Claude Code's under
// ~/.claude/projects and Codex's under ~/.codex/sessions. It is the one figure
// on `logos stats --usage` that is not Logos's own ledger, and it is labelled
// so: tokens the host recorded, priced from a built-in table.
//
// It reads and never writes. The transcripts are the host's files, and the
// totals are recomputed on every run, so there is no cache to fall out of
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
	"github.com/Coder8124/logos/internal/usage"
)

// Usage is token counts as a request was billed: uncached input, the two
// cache-write durations, cache reads, and output.
type Usage struct {
	Input, CacheWrite5m, CacheWrite1h, CacheRead, Output int64
}

// Prompt is everything the model read on a request — its context size.
func (u Usage) Prompt() int64 { return u.Input + u.CacheWrite5m + u.CacheWrite1h + u.CacheRead }

// Total is every token, read and written.
func (u Usage) Total() int64 { return u.Prompt() + u.Output }

func (u *Usage) add(o Usage) {
	u.Input += o.Input
	u.CacheWrite5m += o.CacheWrite5m
	u.CacheWrite1h += o.CacheWrite1h
	u.CacheRead += o.CacheRead
	u.Output += o.Output
}

// Bucket is one row of a breakdown. Unpriced counts the tokens of models with
// no list price, so a total in dollars can say how much it leaves out.
type Bucket struct {
	Usage
	USD      float64
	Unpriced int64
}

func (b *Bucket) add(model string, u Usage) {
	b.Usage.add(u)
	if usd, ok := Cost(model, u); ok {
		b.USD += usd
	} else {
		b.Unpriced += u.Total()
	}
}

// Session is one agent session's largest context, for the session-size panel.
type Session struct {
	Host, Model string
	Start       int64
	Peak        int64
}

// Report is the window read.
type Report struct {
	From             time.Time
	Total            Bucket
	Models, Projects map[string]*Bucket
	// Days is keyed "2006-01-02" in local time.
	Days     map[string]*Bucket
	Sessions []Session
	// Files is how many transcripts were read and Unreadable how many lines
	// in them could not be parsed, so a total missing lines says so. Skipped
	// is the files that could not be read to the end — removed while the walk
	// ran, or holding a line past the scanner's limit — whose lines up to the
	// failure are counted and the rest are not.
	Files, Unreadable, Skipped int
	// Hosts names the hosts whose transcripts were found.
	Hosts []string
}

func newReport(from time.Time) *Report {
	return &Report{From: from, Models: map[string]*Bucket{}, Projects: map[string]*Bucket{}, Days: map[string]*Bucket{}}
}

func (r *Report) add(ts int64, model, project string, u Usage) {
	r.Total.add(model, u)
	for _, kv := range []struct {
		m map[string]*Bucket
		k string
	}{{r.Models, model}, {r.Projects, project}, {r.Days, time.Unix(ts, 0).Format("2006-01-02")}} {
		b := kv.m[kv.k]
		if b == nil {
			b = &Bucket{}
			kv.m[kv.k] = b
		}
		b.add(model, u)
	}
}

// Read totals what Claude Code and Codex recorded from from onward, for one
// project or, when project is "", all of them. A host whose directory is
// missing is skipped. A file that cannot be read to the end is counted in
// Skipped rather than failing the report: one host file deleted mid-walk
// should not blank a month of figures, and the count says what is missing.
func Read(from time.Time, project string) (*Report, error) {
	r := newReport(from)
	if root := transcript.ClaudeProjectsRoot(); root != "" {
		r.Hosts = append(r.Hosts, "Claude Code")
		if err := readClaude(r, root, project); err != nil {
			return nil, err
		}
	}
	if root := transcript.CodexSessionsRoot(); root != "" {
		r.Hosts = append(r.Hosts, "Codex")
		if err := readCodex(r, root, project); err != nil {
			return nil, err
		}
	}
	sort.Slice(r.Sessions, func(i, j int) bool { return r.Sessions[i].Start < r.Sessions[j].Start })
	return r, nil
}

// transcripts lists the .jsonl files under root changed since from. A file
// last written before the window cannot hold a line inside it, and skipping
// it unread is what keeps a month's dashboard off the whole history's bytes.
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

// unknownModel holds tokens whose host did not name the model. They are
// counted, unpriced, rather than dropped, so the token total stays whole.
const unknownModel = "(model not recorded)"

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

// parseTS reports ok=false for a missing or malformed timestamp, which the
// readers count as unreadable: read as zero, it fell before every window and
// the line vanished with no count.
func parseTS(s string) (int64, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}

// projectOf names a session's project by its folder, as the importers do.
// A host's own worktree — <project>/.claude/worktrees/agent-a2ed… — is that
// project's work: named by its folder, every subagent became a project of
// its own and crowded the real ones out of the top five.
func projectOf(cwd string) string {
	cwd = filepath.Clean(cwd)
	if cwd == "" || cwd == "." || cwd == string(filepath.Separator) {
		return "(no project)"
	}
	parts := strings.Split(filepath.ToSlash(cwd), "/")
	for i := len(parts) - 1; i >= 2; i-- {
		if parts[i] == "worktrees" && strings.HasPrefix(parts[i-1], ".") && parts[i-2] != "" {
			return parts[i-2]
		}
	}
	return filepath.Base(cwd)
}

func wanted(project, want string) bool {
	return want == "" || usage.SameProject(project, want)
}

type claudeLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input         int64 `json:"input_tokens"`
			CacheCreation int64 `json:"cache_creation_input_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
			Output        int64 `json:"output_tokens"`
			Split         struct {
				H1 int64 `json:"ephemeral_1h_input_tokens"`
				M5 int64 `json:"ephemeral_5m_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

type claudeReq struct {
	ts             int64
	model, project string
	session        string
	u              Usage
}

// readClaude counts each API request once. Claude Code writes one transcript
// line per content block of a response, each repeating the request's usage,
// so summing lines counts a three-block reply three times; and a resumed
// session copies earlier lines into a new file. The message id and request id
// together name the request, wherever it was written, and the last line
// written for it carries its final output count.
func readClaude(r *Report, root, project string) error {
	files, err := transcripts(root, r.From)
	if err != nil {
		return err
	}
	from := r.From.Unix()
	reqs := map[string]claudeReq{}
	for _, path := range files {
		r.Files++
		err := scanLines(path, func(n int, b []byte) {
			if !bytes.Contains(b, []byte(`"usage"`)) || !bytes.Contains(b, []byte(`"assistant"`)) {
				return
			}
			var ln claudeLine
			if json.Unmarshal(b, &ln) != nil {
				r.Unreadable++
				return
			}
			m := ln.Message
			if ln.Type != "assistant" || m.Usage == nil || m.Model == "" || strings.HasPrefix(m.Model, "<") {
				return // "<synthetic>" is Claude Code's own placeholder, not a request
			}
			ts, ok := parseTS(ln.Timestamp)
			if !ok {
				r.Unreadable++
				return
			}
			p := projectOf(ln.Cwd)
			if ts < from || !wanted(p, project) {
				return
			}
			u := Usage{Input: m.Usage.Input, CacheRead: m.Usage.CacheRead, Output: m.Usage.Output}
			if s := m.Usage.Split; s.H1+s.M5 > 0 {
				u.CacheWrite1h, u.CacheWrite5m = s.H1, s.M5
			} else {
				u.CacheWrite5m = m.Usage.CacheCreation
			}
			// With no message id, two requests in one second of one file
			// would share a timestamp key and the second would replace the
			// first; the line number keeps them apart.
			key := m.ID + "|" + ln.RequestID
			if m.ID == "" {
				key = fmt.Sprintf("%s|%d", path, n)
			}
			// A line with no session id still belongs to its file's session;
			// keyed on "", every such line in every file was one session.
			sess := ln.SessionID
			if sess == "" {
				sess = path
			}
			reqs[key] = claudeReq{ts: ts, model: baseModel(m.Model), project: p, session: sess, u: u}
		})
		if err != nil {
			r.Skipped++
		}
	}
	peaks := map[string]*Session{}
	for _, q := range reqs {
		r.add(q.ts, q.model, q.project, q.u)
		s := peaks[q.session]
		if s == nil {
			s = &Session{Host: "Claude Code", Start: q.ts}
			peaks[q.session] = s
		}
		if q.ts < s.Start {
			s.Start = q.ts
		}
		if q.u.Prompt() > s.Peak {
			s.Peak, s.Model = q.u.Prompt(), q.model
		}
	}
	for _, s := range peaks {
		r.Sessions = append(r.Sessions, *s)
	}
	return nil
}

type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type  string `json:"type"`
		Cwd   string `json:"cwd"`
		Model string `json:"model"`
		Info  *struct {
			Total codexUsage `json:"total_token_usage"`
			Last  codexUsage `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

type codexUsage struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
}

// readCodex counts each file's growth in total_token_usage. Codex repeats a
// token_count event when only its rate limits changed, so adding every
// last_token_usage double-counts; the running total moves only when tokens
// were spent. Its input count includes the cached part, unlike Claude's, so
// the cached and written parts are taken out to leave uncached input.
func readCodex(r *Report, root, project string) error {
	files, err := transcripts(root, r.From)
	if err != nil {
		return err
	}
	from := r.From.Unix()
	for _, path := range files {
		r.Files++
		var model, cwd string
		var prev codexUsage
		sess := Session{Host: "Codex"}
		err := scanLines(path, func(_ int, b []byte) {
			isCtx := bytes.Contains(b, []byte(`"turn_context"`)) || bytes.Contains(b, []byte(`"session_meta"`))
			if !isCtx && !bytes.Contains(b, []byte(`"token_count"`)) {
				return
			}
			var ln codexLine
			if json.Unmarshal(b, &ln) != nil {
				r.Unreadable++
				return
			}
			switch {
			case ln.Type == "turn_context" || ln.Type == "session_meta":
				if ln.Payload.Model != "" {
					model = ln.Payload.Model
				}
				if ln.Payload.Cwd != "" {
					cwd = ln.Payload.Cwd
				}
			case ln.Payload.Type == "token_count" && ln.Payload.Info != nil:
				t := ln.Payload.Info.Total
				d := codexUsage{t.Input - prev.Input, t.Cached - prev.Cached, t.CacheWrite - prev.CacheWrite, t.Output - prev.Output}
				prev = t
				if d.Input+d.Output <= 0 {
					return
				}
				ts, ok := parseTS(ln.Timestamp)
				if !ok {
					r.Unreadable++
					return
				}
				p := projectOf(cwd)
				if ts < from || !wanted(p, project) {
					return
				}
				model := model
				if model == "" {
					model = unknownModel
				}
				u := Usage{Input: max(d.Input-d.Cached-d.CacheWrite, 0), CacheRead: d.Cached, CacheWrite5m: d.CacheWrite, Output: d.Output}
				r.add(ts, model, p, u)
				if sess.Start == 0 {
					sess.Start = ts
				}
				if last := ln.Payload.Info.Last.Input; last > sess.Peak {
					sess.Peak, sess.Model = last, model
				}
			}
		})
		if err != nil {
			r.Skipped++
		}
		if sess.Peak > 0 {
			r.Sessions = append(r.Sessions, sess)
		}
	}
	return nil
}

// Top is the n largest rows of a breakdown, by dollars and then by tokens,
// largest first.
func Top(m map[string]*Bucket, n int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := m[keys[i]], m[keys[j]]
		if a.USD != b.USD {
			return a.USD > b.USD
		}
		if a.Total() != b.Total() {
			return a.Total() > b.Total()
		}
		return keys[i] < keys[j]
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}
