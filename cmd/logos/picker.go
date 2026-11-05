package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

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
//
// checked, when set, makes it a checklist: Space ticks the selected row, and
// the ticks are left in checked for the caller, which shares its backing
// array with the copy the picker was given.
//
// submit, set on a checklist, adds a last row that ends the menu, and Enter
// or a click on any other row ticks it instead. That is opt-in: in every
// other menu Enter runs the highlighted row, and the flag menus and resume
// depend on it.
//
// field makes one row a text box that Enter or a click opens, as the "other"
// answer does in an agent's question menu. mouse turns on click reporting
// while the menu is up.
type menu struct {
	title, hint string
	rows        []string
	sel, over   int
	checked     []bool
	submit      string
	field       *menuField
	mouse       bool
}

// menuField is a row typed into. check cleans what was typed or says what is
// wrong with it; the menu shows the reason and stays open on the text, so a
// mistyped folder is fixed in place rather than discovered after the run.
// value is left set for the caller, and the row ticked when it is not empty.
type menuField struct {
	row           int
	prompt, value string
	check         func(string) (string, error)
}

// keyboard is stdin, read through one buffer for every menu. A fast typist's
// Enter, Enter can arrive in one read; a reader per menu kept the second Enter
// in the first menu's buffer and threw it away with it.
var keyboard = bufio.NewReader(os.Stdin)

// keyboardFrom is the stdin keyboard was built over, so a prompt after a menu
// reads on from keyboard's buffer instead of past it.
var keyboardFrom = os.Stdin

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
	if m.mouse {
		// SGR reporting (1006), because the old encoding cannot say a row
		// past 223. Off again before the shell gets the terminal back: left
		// on, every click in it after logos exits types escape codes.
		fmt.Print("\x1b[?1000h\x1b[?1006h")
		defer fmt.Print("\x1b[?1000l\x1b[?1006l")
	}
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
	n := len(rows)
	if m.submit != "" {
		n++ // the submit row, drawn last and never ticked
	}
	sel, top, up, drawn := min(max(m.sel, 0), n-1), 0, m.over, 0
	editing, typed, msg := false, []byte(nil), ""
	// A click says where on the screen it landed, not which row, so each
	// frame asks the terminal where its cursor ended up. pending is the
	// height of every frame still waiting on its answer, oldest first, and
	// titleAt the screen row the newest answered frame put its title on.
	var pending []int
	titleAt := 0
	label := func(i int) string {
		switch {
		case i == len(rows):
			return m.submit
		case m.field != nil && i == m.field.row && editing:
			// The end of what is typed, not the start, when it is too long:
			// a path grows on the right, and cut there the cursor is gone.
			r := []rune(string(typed) + "█")
			if room := width - 1 - 6 - utf8.RuneCountInString(m.field.prompt); len(r) > room && room > 1 {
				r = append([]rune("…"), r[len(r)-room+1:]...)
			}
			return m.field.prompt + string(r)
		case m.field != nil && i == m.field.row && m.field.value != "":
			return rows[i] + ": " + m.field.value
		}
		return rows[i]
	}
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
		for i := top; i < n && i < top+pickerWindow; i++ {
			mark := "  "
			if i == sel {
				mark = "> "
			}
			if m.checked != nil && i < len(rows) {
				if m.checked[i] {
					mark += "[x] "
				} else {
					mark += "[ ] "
				}
			}
			lines = append(lines, text.Ellipsize(mark+label(i), width-1))
		}
		if n > pickerWindow {
			lines = append(lines, fmt.Sprintf("  (%d of %d)", sel+1, n))
		}
		if msg != "" {
			lines = append(lines, text.Ellipsize("  "+msg, width-1))
		}
		for _, l := range lines {
			fmt.Fprintf(out, "\x1b[2K%s\r\n", l)
		}
		// Clear to the end of the screen: a frame drawn over a taller one
		// (the conversations over themselves and the agent menu) would
		// otherwise leave the old one's last lines under it.
		fmt.Fprint(out, "\x1b[J")
		up, drawn = len(lines), len(lines)
		if m.mouse {
			fmt.Fprint(out, "\x1b[6n")
			pending = append(pending, drawn)
		}
	}
	// settle reads the answers to frames drawn just before the menu ended.
	// Left in the buffer, an answer is the first thing the next prompt reads,
	// and "\x1b[14;1Ry" is not a yes. Enter or Ctrl-C ends the wait, so a
	// terminal that never answers costs a keypress, not a hang.
	settle := func() {
		for len(pending) > 0 {
			b, err := in.ReadByte()
			if err != nil || b == '\r' || b == 3 {
				return
			}
			if b == 0x1b {
				if next, err := in.ReadByte(); err == nil && next == '[' {
					if _, final := readCSI(in); final == 'R' {
						pending = pending[1:]
					}
				}
			}
		}
	}
	// activate is Enter on the highlighted row, or a click on it; done is
	// whether the menu ends with it.
	activate := func() (done bool) {
		switch {
		case m.submit != "" && sel == len(rows):
			for _, c := range m.checked {
				if c {
					return true
				}
			}
			msg = "Tick at least one row first."
		case m.field != nil && sel == m.field.row:
			editing, typed = true, []byte(m.field.value)
		case m.submit != "":
			m.checked[sel] = !m.checked[sel]
		default:
			return true
		}
		return false
	}
	// accept checks what was typed; a refusal keeps the field open on it. In
	// a checklist the answer ticks its row and the menu stays up for the
	// rest; anywhere else the row was the choice, so an answer ends the menu
	// the way Enter on any other row does.
	accept := func() (done bool) {
		v, err := m.field.check(string(typed))
		if err != nil {
			msg = err.Error() + " — fix it, or Esc to leave the field"
			return false
		}
		m.field.value, editing = v, false
		if m.checked != nil {
			m.checked[m.field.row] = v != ""
			return false
		}
		return v != ""
	}
	// escape reads what follows an ESC and handles it. It reports whether
	// the screen changed, and how the menu ends if it does.
	escape := func() (dirty bool, end pick, ended bool) {
		// An arrow arrives as ESC [ A (or ESC O A in application mode) in
		// one write, so its tail is already buffered; an ESC with nothing
		// after it is the key itself.
		lone := in.Buffered() == 0
		if !lone {
			next, _ := in.Peek(1)
			lone = next[0] != '[' && next[0] != 'O'
		}
		if lone {
			if editing {
				editing = false
				return true, 0, false
			}
			return false, wentBack, true
		}
		intro, _ := in.ReadByte()
		var params string
		var final byte
		if intro == 'O' {
			final, _ = in.ReadByte()
		} else {
			params, final = readCSI(in)
		}
		switch {
		case final == 'R' && len(pending) > 0:
			var r, c int
			if _, err := fmt.Sscanf(params, "%d;%d", &r, &c); err == nil {
				titleAt = r - pending[0]
			}
			pending = pending[1:]
			return false, 0, false
		case final == 'M' && strings.HasPrefix(params, "<"):
			var b, x, y int
			if _, err := fmt.Sscanf(params, "<%d;%d;%d", &b, &x, &y); err != nil {
				return false, 0, false
			}
			switch b {
			case 64:
				sel = max(sel-1, 0)
			case 65:
				sel = min(sel+1, n-1)
			case 0:
				k := y - titleAt
				if titleAt == 0 || k < 1 || k > pickerWindow || top+k-1 >= n {
					return false, 0, false // the title, or off the menu
				}
				row := top + k - 1
				if editing {
					if row == m.field.row {
						return false, 0, false
					}
					editing = false // a click elsewhere leaves the field as it was
				}
				sel, msg = row, ""
				if activate() {
					return true, picked, true
				}
			}
			return true, 0, false
		case editing:
			return false, 0, false
		case final == 'A':
			sel = max(sel-1, 0)
		case final == 'B':
			sel = min(sel+1, n-1)
		}
		return true, 0, false
	}
	draw()
	for {
		b, err := in.ReadByte()
		if err != nil {
			return 0, cancelled, drawn // input closed: nobody is there to choose
		}
		dirty := true
		if b != 0x1b {
			msg = ""
		}
		switch {
		case b == 3 || b == 4: // Ctrl-C, Ctrl-D, in the field as anywhere
			return 0, cancelled, drawn
		case b == 0x1b:
			d, end, ended := escape()
			if ended {
				if end != picked {
					return 0, end, drawn
				}
				settle()
				return sel, picked, drawn
			}
			dirty = d
		case editing && (b == '\r' || b == '\n'):
			if accept() {
				settle()
				return sel, picked, drawn
			}
		case editing && (b == 0x7f || b == 8): // Backspace takes a whole character
			if len(typed) > 0 {
				_, size := utf8.DecodeLastRune(typed)
				typed = typed[:len(typed)-size]
			}
		case editing && b >= 0x20:
			typed = append(typed, b)
		case editing:
		case b == '\r' || b == '\n':
			if activate() {
				settle()
				return sel, picked, drawn
			}
		case b == 'q':
			return 0, cancelled, drawn
		case b == 'k':
			sel = max(sel-1, 0)
		case b == 'j':
			sel = min(sel+1, n-1)
		case b == ' ' && m.field != nil && sel == m.field.row && m.field.value == "":
			activate() // nothing to tick until a folder is named
		case b == ' ' && m.checked != nil && sel < len(rows):
			m.checked[sel] = !m.checked[sel]
		}
		// Once per batch of keys, not per key: held arrows redraw once. A
		// cursor report changes nothing, and redrawing for it would ask
		// again, forever.
		if dirty && in.Buffered() == 0 {
			draw()
		}
	}
}

// readCSI reads the rest of a control sequence after ESC [: its parameters,
// then the byte that ends it. A click and a cursor report are longer than the
// one byte an arrow needs, and reading one byte left the rest to be taken as
// keys.
func readCSI(in *bufio.Reader) (string, byte) {
	var params []byte
	for {
		c, err := in.ReadByte()
		if err != nil {
			return string(params), 0
		}
		if c >= 0x40 && c <= 0x7e {
			return string(params), c
		}
		params = append(params, c)
	}
}

// cancelledPick is said when the picker is dismissed, so a cancel is never
// mistaken for an empty project.
func cancelledPick() {
	fmt.Println("Nothing resumed — the picker was cancelled.")
}
