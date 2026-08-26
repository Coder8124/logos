package adapters

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/pragun/brain/internal/eval"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Bridge runs a memory system that is not written in Go.
//
// The systems worth comparing against — mem0, mempalace — are Python. Rather
// than reimplementing them (which would compare this project against my reading
// of their README) each runs as a subprocess in its own virtualenv, speaking
// newline-delimited JSON on stdin and stdout. The shim on the other side is
// thin on purpose: it translates events into that system's own API and gets out
// of the way, so what is measured is their retrieval, not my wrapper.
//
// Both are pointed at Ollama, so no API keys are needed and nothing leaves the
// machine. That also means the comparison is like-for-like: every system in the
// table is running against the same local models on the same hardware.
type Bridge struct {
	label string
	path  string

	cmd *exec.Cmd
	in  *bufio.Writer
	out *bufio.Scanner
}

type bridgeReply struct {
	OK    bool   `json:"ok"`
	Text  string `json:"text"`
	Error string `json:"error"`
}

// BridgeDir is where the Python shims live, relative to the repo root.
const BridgeDir = "bench/adapters"

// Discover returns a Bridge for every shim that reports itself runnable.
//
// A shim that cannot import its own package is skipped, not failed: the common
// case is that the user has not installed that system, and a missing row is
// honest where a row of zeros would be a lie about the system's quality.
func Discover() []*Bridge {
	dir := findBridgeDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_adapter.py") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var out []*Bridge
	for _, name := range names {
		path := filepath.Join(dir, name)
		label := strings.TrimSuffix(name, "_adapter.py")
		if reason := probe(path); reason != "" {
			fmt.Fprintf(os.Stderr, "· skipping %s: %s\n", label, reason)
			continue
		}
		out = append(out, &Bridge{label: label, path: path})
	}
	return out
}

// probe asks a shim whether it can run, with a short timeout so a system that
// hangs on import cannot hang the benchmark.
func probe(path string) string {
	cmd := exec.Command(pythonFor(path), path, "--probe")
	done := make(chan error, 1)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		return err.Error()
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			line := strings.TrimSpace(out.String())
			if i := strings.LastIndex(line, "\n"); i >= 0 {
				line = line[i+1:]
			}
			if line == "" {
				line = err.Error()
			}
			return line
		}
		return ""
	case <-time.After(90 * time.Second):
		cmd.Process.Kill()
		return "probe timed out"
	}
}

// pythonFor prefers a virtualenv sitting next to the shim, so each system can
// pin its own dependency tree without any of them colliding.
func pythonFor(shim string) string {
	venv := filepath.Join(filepath.Dir(shim), ".venv-"+strings.TrimSuffix(filepath.Base(shim), "_adapter.py"), "bin", "python")
	if _, err := os.Stat(venv); err == nil {
		return venv
	}
	return "python3"
}

func findBridgeDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, BridgeDir)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func (b *Bridge) Name() string { return b.label }

func (b *Bridge) start() error {
	cmd := exec.Command(pythonFor(b.path), b.path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	b.cmd = cmd
	b.in = bufio.NewWriter(stdin)
	b.out = bufio.NewScanner(stdout)
	// Responses carry whole retrieved contexts, which comfortably exceed the
	// scanner's default 64K line limit.
	b.out.Buffer(make([]byte, 0, 1<<20), 16<<20)
	return nil
}

func (b *Bridge) call(req map[string]any) (bridgeReply, error) {
	if b.cmd == nil {
		if err := b.start(); err != nil {
			return bridgeReply{}, err
		}
	}
	line, err := json.Marshal(req)
	if err != nil {
		return bridgeReply{}, err
	}
	if _, err := b.in.Write(append(line, '\n')); err != nil {
		return bridgeReply{}, err
	}
	if err := b.in.Flush(); err != nil {
		return bridgeReply{}, err
	}
	if !b.out.Scan() {
		if err := b.out.Err(); err != nil {
			return bridgeReply{}, err
		}
		return bridgeReply{}, fmt.Errorf("%s exited mid-run", b.label)
	}
	var reply bridgeReply
	if err := json.Unmarshal(b.out.Bytes(), &reply); err != nil {
		return bridgeReply{}, fmt.Errorf("%s sent unparseable output: %w", b.label, err)
	}
	if !reply.OK {
		return reply, fmt.Errorf("%s: %s", b.label, reply.Error)
	}
	return reply, nil
}

func (b *Bridge) Reset() error {
	_, err := b.call(map[string]any{"op": "reset"})
	return err
}

// Write hands the event over in both forms: the structured fields, for a system
// that can use them, and Flatten's prose, for one that cannot. Nothing is
// withheld from a system because its API is simpler than brain's.
func (b *Bridge) Write(ev eval.Event) error {
	_, err := b.call(map[string]any{"op": "write", "event": map[string]any{
		"ts": ev.TS, "actor": ev.Actor, "kind": string(ev.Kind), "project": ev.Project,
		"title": ev.Title, "text": ev.Text, "task": ev.Task,
		"decisions": ev.Decisions, "failed": ev.Failed, "questions": ev.Questions,
		"next": ev.Next, "flat": ev.Flatten(),
	}})
	return err
}

func (b *Bridge) Read(q eval.Query) (eval.Response, error) {
	reply, err := b.call(map[string]any{"op": "read", "query": map[string]any{
		"task": q.Task, "project": q.Project, "agent": q.Agent, "budget": q.Budget, "now": q.Now,
	}})
	if err != nil {
		return eval.Response{}, err
	}
	return eval.Response{Text: reply.Text}, nil
}

func (b *Bridge) Close() error {
	if b.cmd == nil {
		return nil
	}
	b.call(map[string]any{"op": "close"})
	b.cmd.Process.Kill()
	b.cmd.Wait()
	b.cmd = nil
	return nil
}
