// Package capture is the episodic tier: sampling, coalescing, storage and
// retention.
//
// The event record itself lives in internal/event so that this package and the
// sources it drives can share it without an import cycle. These aliases keep
// capture.Event working as the natural spelling at call sites.
package capture

import "github.com/Coder8124/brain/internal/event"

type (
	Event = event.Event
	Kind  = event.Kind
)

const (
	Focus     = event.Focus
	URL       = event.URL
	File      = event.File
	Commit    = event.Commit
	Calendar  = event.Calendar
	Clipboard = event.Clipboard
	Screen    = event.Screen
)

const IncidentalSecs = event.IncidentalSecs

var Now = event.Now
