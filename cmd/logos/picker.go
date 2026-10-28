package main

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/text"
)

// interactive reports whether a person is at both ends of this process. A
// var so a test can claim a terminal it does not have, and prove the hook
// path refuses the picker even then.
var interactive = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// pickerAllowed is whether a bare `logos resume <project>` may stop and ask.
//
// The SessionStart hook runs this exact command and injects its stdout, so a
// picker there would wait on a keypress nobody can send until the host kills
// the hook — and lose the handoff the hook exists to deliver. Its output is
// a pipe, so the terminal test alone already refuses it; LOGOS_HOOK is
// checked as well because it is the signal every hook path sets on purpose,
// and a terminal check is a property of how the host happens to spawn hooks
// today, not a promise. --json is a script, never a person.
func pickerAllowed(asJSON bool) bool {
	if asJSON || inHook || os.Getenv("LOGOS_HOOK") != "" {
		return false
	}
	return interactive()
}

// pickConversation shows the picker on the terminal and returns the chosen
// conversation, nil for "the whole project", and ok=false for a cancel.
func pickConversation(projectName string, convs []session.Conversation) (*session.Conversation, bool, error) {
	fd := int(os.Stdin.Fd())
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		width = w
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return nil, false, err
	}
	defer term.Restore(fd, old)
	rows := make([]string, 0, len(convs)+1)
	// First, and preselected: Enter alone does exactly what this command
	// did before the picker existed, so nobody's muscle memory breaks.
	rows = append(rows, "the whole project — every conversation, memories and open work")
	for _, c := range convs {
		rows = append(rows, session.ConversationLine(c))
	}
	i, ok := runPicker(os.Stdin, os.Stdout, fmt.Sprintf("Resume which conversation in %s?", projectName), rows, width)
	if !ok || i == 0 {
		return nil, ok, nil
	}
	return &convs[i-1], true, nil
}

// pickerWindow is how many rows show at once. A project with hundreds of
// conversations must not scroll the user's terminal away.
const pickerWindow = 10

// runPicker is the picker's loop over plain reader and writer, so it can be
// driven by a test without a terminal. Output is "\r\n"-terminated because a
// raw-mode terminal does not turn "\n" into a carriage return.
func runPicker(in io.Reader, out io.Writer, title string, rows []string, width int) (int, bool) {
	sel, top, drawn := 0, 0, 0
	draw := func() {
		if drawn > 0 {
			fmt.Fprintf(out, "\x1b[%dA", drawn)
		}
		if sel < top {
			top = sel
		}
		if sel >= top+pickerWindow {
			top = sel - pickerWindow + 1
		}
		// Every line is cut to the width, the title too: the redraw moves up
		// one row per line, and a line that wraps leaves debris behind.
		lines := []string{text.Ellipsize(title+"  (↑/↓ to move, Enter to resume, q to cancel)", width-1)}
		for i := top; i < len(rows) && i < top+pickerWindow; i++ {
			mark := "  "
			if i == sel {
				mark = "> "
			}
			lines = append(lines, text.Ellipsize(mark+rows[i], width-1))
		}
		if len(rows) > pickerWindow {
			lines = append(lines, fmt.Sprintf("  (%d of %d)", sel+1, len(rows)))
		}
		for _, l := range lines {
			fmt.Fprintf(out, "\x1b[2K%s\r\n", l)
		}
		drawn = len(lines)
	}
	draw()
	buf := make([]byte, 64)
	for {
		n, err := in.Read(buf)
		if n == 0 && err != nil {
			return 0, false // input closed: nobody is there to choose
		}
		for k := 0; k < n; k++ {
			switch b := buf[k]; {
			case b == '\r' || b == '\n':
				return sel, true
			case b == 'q' || b == 3 || b == 4: // q, Ctrl-C, Ctrl-D
				return 0, false
			case b == 'k':
				sel = max(sel-1, 0)
			case b == 'j':
				sel = min(sel+1, len(rows)-1)
			case b == 0x1b:
				// An arrow arrives as ESC [ A (or ESC O A in application
				// mode) in one read; an ESC with nothing after it is the key
				// itself, a cancel.
				if k+2 < n && (buf[k+1] == '[' || buf[k+1] == 'O') {
					switch buf[k+2] {
					case 'A':
						sel = max(sel-1, 0)
					case 'B':
						sel = min(sel+1, len(rows)-1)
					}
					k += 2
					continue
				}
				return 0, false
			}
		}
		draw()
	}
}

// cancelledPick is said when the picker is dismissed, so a cancel is never
// mistaken for an empty project.
func cancelledPick() {
	fmt.Println("Nothing resumed — the picker was cancelled.")
}
