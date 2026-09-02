// Package sources pulls episodic events off the machine.
package sources

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/event"
)

// Frontmost reads the active application and window title.
//
// Uses osascript rather than linking the Accessibility API through cgo. That is
// a deliberate v1 tradeoff: no build-time ObjC bindings, degrades to
// app-name-only when the AX permission is denied, and costs one short-lived
// subprocess per poll — negligible at a 5s interval. Swap for a native AX
// binding if the poll rate ever needs to drop below ~1s.
type Frontmost struct {
	Granularity Granularity
}

type Granularity int

const (
	// AppOnly: Accessibility denied. Coarser, still useful.
	AppOnly Granularity = iota
	// AppAndTitle: Accessibility granted.
	AppAndTitle
)

const PollInterval = 5 * time.Second

const appScript = `tell application "System Events" to name of first application process whose frontmost is true`

const appAndTitleScript = `tell application "System Events"
    set p to first application process whose frontmost is true
    set n to name of p
    try
        set w to name of front window of p
    on error
        set w to ""
    end try
end tell
return n & "\n" & w`

func osascript(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return "", fmt.Errorf("osascript: %w", err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// ProbeFrontmost checks once at startup what this machine will actually yield,
// so the daemon can tell the user rather than silently capturing less than they
// expect.
func ProbeFrontmost() *Frontmost {
	if out, err := osascript(appAndTitleScript); err == nil && out != "" {
		return &Frontmost{Granularity: AppAndTitle}
	}
	return &Frontmost{Granularity: AppOnly}
}

func (f *Frontmost) Sample() (event.Event, error) {
	ts := event.Now()

	if f.Granularity == AppOnly {
		app, err := osascript(appScript)
		if err != nil {
			return event.Event{}, err
		}
		return event.Event{TS: ts, Kind: event.Focus, App: app}, nil
	}

	out, err := osascript(appAndTitleScript)
	if err != nil {
		return event.Event{}, err
	}
	app, title, _ := strings.Cut(out, "\n")
	return event.Event{
		TS:    ts,
		Kind:  event.Focus,
		App:   app,
		Title: strings.TrimSpace(title),
	}, nil
}
