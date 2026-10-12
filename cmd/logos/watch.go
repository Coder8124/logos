package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/activity"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/secret"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// runWatch is `logos watch`: captures, rulings handed back and memory changes,
// printed as they happen until Ctrl-C.
func runWatch(args []string) error {
	vault, err := requireVault()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return watchVault(ctx, vault, os.Stdout, 300*time.Millisecond)
}

// watchVault tails the three append-only records in the vault rather than the
// index: a hook, an MCP server and the CLI all write them, from separate
// processes, and the files are what every one of them is guaranteed to have
// written. Polled, not notified — a third-party watcher for a demo command is
// a dependency, and a stat every 300ms is nothing.
func watchVault(ctx context.Context, vault string, w io.Writer, every time.Duration) error {
	tails := []*tail{
		{glob: filepath.Join(vault, activity.Dir, "*.jsonl"), line: captureLine},
		{glob: filepath.Join(vault, usagepkg.Dir, "*.jsonl"), line: usageLine},
		{glob: filepath.Join(vault, memory.Dir, memory.LogFile), line: timelineLine},
	}
	for _, t := range tails {
		t.skipToEnd()
	}
	// A watch that can never print a capture would sit silent and read as a
	// broken command, so it says which of its sources are switched off.
	if !activity.Recording(vault) {
		fmt.Fprintln(w, "· captures are not being recorded in this vault, so none will show; `logos activity on` starts them")
	}
	if !usagepkg.Recording(vault) {
		fmt.Fprintln(w, "· the usage ledger is off, so rulings handed back will not show; `logos usage on` starts it")
	}
	fmt.Fprintf(w, "Watching %s — captures, rulings handed back, memory changes. Ctrl-C stops.\n", vault)

	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		for _, t := range tails {
			for _, msg := range t.poll() {
				fmt.Fprintf(w, "%s  %s\n", time.Now().Format("15:04:05"), msg)
			}
		}
	}
}

// tail follows every file a glob matches, a line at a time. A month's file
// that appears after the watch started is read from its start: everything in
// it is new.
type tail struct {
	glob    string
	line    func(string) string
	offsets map[string]int64
}

func (t *tail) skipToEnd() {
	t.offsets = map[string]int64{}
	files, _ := filepath.Glob(t.glob)
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			t.offsets[f] = st.Size()
		}
	}
}

// poll returns the formatted lines appended since the last call. Only whole
// lines: a writer caught mid-line is read on the next tick, not split in two.
func (t *tail) poll() []string {
	files, _ := filepath.Glob(t.glob)
	var out []string
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		at := t.offsets[f]
		if st.Size() < at {
			// Rewritten, not appended to — a project rename rewrites the
			// timeline. Replaying it would print the whole history as news.
			t.offsets[f] = st.Size()
			out = append(out, fmt.Sprintf("· %s was rewritten; watching from its new end", filepath.Base(f)))
			continue
		}
		if st.Size() == at {
			continue
		}
		fh, err := os.Open(f)
		if err != nil {
			out = append(out, fmt.Sprintf("· could not read %s: %v", filepath.Base(f), err))
			continue
		}
		buf := make([]byte, st.Size()-at)
		n, err := fh.ReadAt(buf, at)
		fh.Close()
		if err != nil && err != io.EOF {
			out = append(out, fmt.Sprintf("· could not read %s: %v", filepath.Base(f), err))
			continue
		}
		buf = buf[:n]
		end := strings.LastIndexByte(string(buf), '\n')
		if end < 0 {
			continue
		}
		t.offsets[f] = at + int64(end) + 1
		for _, l := range strings.Split(string(buf[:end]), "\n") {
			if msg := t.line(l); msg != "" {
				out = append(out, msg)
			}
		}
	}
	return out
}

// The lines below print vault text straight to a terminal, so each field goes
// through terminalSafe: an escape sequence in a prompt or a memory would be
// obeyed by the terminal, not shown.

func captureLine(raw string) string {
	var e activity.Event
	if json.Unmarshal([]byte(raw), &e) != nil || e.Kind == "" {
		return ""
	}
	msg := "capture  " + terminalSafe(e.Kind, 20)
	if e.Summary != "" {
		msg += ": " + terminalSafe(e.Summary, 160)
	}
	if e.Private > 0 {
		msg += " — " + secret.Summary(secret.KeptPrivate("", e.Private))
	}
	return msg + inProject(e.Project)
}

func usageLine(raw string) string {
	var e usagepkg.Event
	if json.Unmarshal([]byte(raw), &e) != nil {
		return ""
	}
	var msg string
	switch e.Kind {
	case usagepkg.KindDeadEnd:
		what := "approaches"
		if e.Rulings == 1 {
			what = "approach"
		}
		msg = fmt.Sprintf("ruling   %d ruled-out %s handed back before a retry", e.Rulings, what)
	case usagepkg.KindAtEdit:
		msg = fmt.Sprintf("ruling   %d %s naming a file shown as it was edited", e.Rulings, plural(e.Rulings, "ruling"))
	case usagepkg.KindAvoided:
		msg = fmt.Sprintf("ruling   a session ended without touching what %d %s it was shown named", e.Rulings, plural(e.Rulings, "ruling"))
	case usagepkg.KindPack:
		msg = fmt.Sprintf("context  %d-token pack handed over", e.Sent)
		if e.Full > e.Sent {
			msg += fmt.Sprintf(", %d left out for space", e.Full-e.Sent)
		}
	default:
		return ""
	}
	if e.Via != "" {
		msg += " (" + terminalSafe(e.Via, 40) + ")"
	}
	return msg + inProject(e.Project)
}

func timelineLine(raw string) string {
	got := memory.ParseLog(raw)
	if len(got) == 0 {
		return ""
	}
	e := got[0]
	msg := fmt.Sprintf("memory   #%d %s", e.MemID, terminalSafe(e.Event, 20))
	if e.RefID != 0 {
		msg += fmt.Sprintf(" (#%d)", e.RefID)
	}
	return msg + ": " + terminalSafe(e.Detail, 160) + inProject(e.Project)
}

func inProject(project string) string {
	if project == "" {
		return ""
	}
	return " [" + terminalSafe(project, 40) + "]"
}
