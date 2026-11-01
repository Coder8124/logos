package main

import (
	"bufio"
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

// chooseResume runs the conversation picker and then, for one conversation,
// the agent menu. It returns the conversation (nil for the whole project),
// the agent to start on it (nil to print the handoff here), and ok=false for
// a cancel. Esc in the agent menu goes back to the conversations, so a wrong
// pick costs a keypress, not a rerun.
func chooseResume(projectName string, convs []session.Conversation) (*session.Conversation, *launcher, bool, error) {
	rows := make([]string, 0, len(convs)+1)
	// First, and preselected: Enter alone does exactly what this command
	// did before the picker existed, so nobody's muscle memory breaks.
	rows = append(rows, "the whole project — every conversation, memories and open work")
	for _, c := range convs {
		rows = append(rows, session.ConversationLine(c))
	}
	conversations := menu{title: fmt.Sprintf("Resume which conversation in %s?", projectName), hint: "↑/↓ to move, Enter to resume, q to cancel", rows: rows}
	// Looked up once a conversation is chosen, not before the first menu:
	// finding Claude Code can mean walking VS Code's extensions folder, and
	// someone who only wants the whole project should not wait on that.
	var agents []launcher
	looked := false
	for {
		i, res, lines, err := pickOnTerminal(conversations)
		switch {
		case err != nil:
			return nil, nil, false, err
		case res != picked:
			// Nothing comes before this menu, so going back from it is a cancel.
			return nil, nil, false, nil
		case i == 0:
			return nil, nil, true, nil
		}
		c := &convs[i-1]
		if !looked {
			agents, looked = availableLaunchers(), true
		}
		if len(agents) == 0 {
			return c, nil, true, nil
		}
		l, res, agentLines, err := pickAgent(agents)
		switch {
		case err != nil:
			// The first picker had the terminal a moment ago, so this is
			// unlikely; printing the handoff is what the pick asked for at
			// the least, and the reason the menu did not show is said.
			fmt.Fprintf(os.Stderr, "logos: could not show the agent menu (%v) — printing the handoff here\n", err)
			return c, nil, true, nil
		case res == cancelled:
			return nil, nil, false, nil
		case res == wentBack:
			// Drawn over both frames, on the row that was chosen: a fresh
			// list under the old ones scrolled the terminal a menu per Esc
			// and made the person find their place again.
			conversations.sel, conversations.over = i, lines+agentLines
			continue
		}
		return c, l, true, nil
	}
}

// pickAgent asks where a chosen conversation continues: here, as the printed
// handoff, or in a new session of one of the agents on this machine. Printing
// is first and preselected, so Enter, Enter is what Enter was before this
// menu existed. The title does not repeat the id: the conversation is still
// on screen just above, and a Claude Code id alone would push the keys off
// an 80-column line.
func pickAgent(agents []launcher) (*launcher, pick, int, error) {
	rows := []string{"print the handoff here"}
	for _, l := range agents {
		rows = append(rows, "start "+l.Name)
	}
	i, res, lines, err := pickOnTerminal(menu{title: "Continue it where?", hint: "Enter to choose, Esc to go back, q to cancel", rows: rows})
	if err != nil || res != picked || i == 0 {
		return nil, res, lines, err
	}
	return &agents[i-1], picked, lines, nil
}

// menu is one list on screen: what it asks, its rows, the row selected when it
// opens, and how many lines above the cursor it is drawn over (0 for below
// whatever is there).
type menu struct {
	title, hint string
	rows        []string
	sel, over   int
}

// keyboard is stdin, read through one buffer for every menu. A fast typist's
// Enter, Enter can arrive in one read; a reader per menu kept the second Enter
// in the first menu's buffer and threw it away with it.
var keyboard = bufio.NewReader(os.Stdin)

// pickOnTerminal is runPicker on the real terminal, in raw mode for exactly
// as long as the menu is up. It returns the row, how the menu was left, and
// how many lines it drew. A var so a test can play the keypresses of two
// menus in a row, which runPicker alone cannot show.
var pickOnTerminal = func(m menu) (int, pick, int, error) {
	fd := int(os.Stdin.Fd())
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		width = w
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return 0, cancelled, 0, err
	}
	defer term.Restore(fd, old)
	i, res, lines := runPicker(keyboard, os.Stdout, m, width)
	return i, res, lines, nil
}

// pick is how a menu was left. Back and cancel are apart because Esc in a
// submenu means "not this one", and reading it as a cancel would throw away
// the conversation the person just chose.
type pick int

const (
	picked pick = iota
	cancelled
	wentBack
)

// pickerWindow is how many rows show at once. A project with hundreds of
// conversations must not scroll the user's terminal away.
const pickerWindow = 10

// runPicker is the picker's loop over plain reader and writer, so it can be
// driven by a test without a terminal. Output is "\r\n"-terminated because a
// raw-mode terminal does not turn "\n" into a carriage return.
func runPicker(in *bufio.Reader, out io.Writer, m menu, width int) (int, pick, int) {
	rows := m.rows
	sel, top, up, drawn := min(max(m.sel, 0), len(rows)-1), 0, m.over, 0
	draw := func() {
		if up > 0 {
			fmt.Fprintf(out, "\x1b[%dA", up)
		}
		if sel < top {
			top = sel
		}
		if sel >= top+pickerWindow {
			top = sel - pickerWindow + 1
		}
		// Every line is cut to the width, the title too: the redraw moves up
		// one row per line, and a line that wraps leaves debris behind.
		lines := []string{text.Ellipsize(m.title+"  ("+m.hint+")", width-1)}
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
		// Clear to the end of the screen: a frame drawn over a taller one
		// (the conversations over themselves and the agent menu) would
		// otherwise leave the old one's last lines under it.
		fmt.Fprint(out, "\x1b[J")
		up, drawn = len(lines), len(lines)
	}
	draw()
	for {
		b, err := in.ReadByte()
		if err != nil {
			return 0, cancelled, drawn // input closed: nobody is there to choose
		}
		switch {
		case b == '\r' || b == '\n':
			return sel, picked, drawn
		case b == 'q' || b == 3 || b == 4: // q, Ctrl-C, Ctrl-D
			return 0, cancelled, drawn
		case b == 'k':
			sel = max(sel-1, 0)
		case b == 'j':
			sel = min(sel+1, len(rows)-1)
		case b == 0x1b:
			// An arrow arrives as ESC [ A (or ESC O A in application mode)
			// in one write, so its tail is already buffered; an ESC with
			// nothing after it is the key itself, which goes back.
			if in.Buffered() == 0 {
				return 0, wentBack, drawn
			}
			if next, _ := in.Peek(1); next[0] != '[' && next[0] != 'O' {
				return 0, wentBack, drawn
			}
			in.Discard(1)
			switch c, _ := in.ReadByte(); c {
			case 'A':
				sel = max(sel-1, 0)
			case 'B':
				sel = min(sel+1, len(rows)-1)
			}
		}
		// Once per batch of keys, not per key: held arrows redraw once.
		if in.Buffered() == 0 {
			draw()
		}
	}
}

// cancelledPick is said when the picker is dismissed, so a cancel is never
// mistaken for an empty project.
func cancelledPick() {
	fmt.Println("Nothing resumed — the picker was cancelled.")
}
