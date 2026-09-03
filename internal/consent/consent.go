// Package consent tracks whether the app is allowed to call agent.Learn after
// a chat exchange without asking first.
//
// The gate this package backs is narrow on purpose: it only covers automatic
// learning from conversation (Stage 4.2). MCP writes have their own,
// separately-scoped default in internal/mcpserver (BRAIN_TRUST_MCP), and
// manual/CLI writes are never gated at all. Conflating the three would mean
// one on/off switch governing every write path in the app, which is exactly
// the kind of blanket toggle that either nags on every turn or gets flipped
// on once and forgotten — neither of which is "ask before learning."
//
// Asking before every single Learn would be correct but exhausting: a chat
// session is many exchanges, and re-prompting after each one trains the user
// to click through without reading, which defeats the point of asking at
// all. So consent here is not a single bit — it is a bit plus an expiry.
// Grant(d) says "stop asking for a while," Revoke undoes that early, and
// Allowed reports whether the grant is still live. This mirrors how the rest
// of the app treats trust as a scoped, time-bounded thing rather than a
// permanent setting: see internal/mcpserver's BRAIN_TRUST_MCP for the sibling
// decision on the MCP side.
package consent

import (
	"sync"
	"time"
)

// state is package-level and mutex-protected rather than a struct threaded
// through app.App: chat.go's Send runs in its own goroutine per message and
// there is exactly one conversation per running app, so a shared package
// state is no heavier than a field on App would be, without requiring every
// caller (chat.go today, potentially cmd/brain tomorrow) to carry a *App
// reference just to ask "am I allowed to learn right now."
var (
	mu      sync.Mutex
	until   time.Time // zero means "not granted"
	forever bool      // Grant(0) or a negative duration: granted for the rest of this run
)

// Allowed reports whether automatic learning may proceed right now without
// asking. It is false until something calls Grant, and reverts to false once
// a timed grant expires — the caller (chat.go) is expected to re-ask at that
// point rather than silently resume learning.
func Allowed() bool {
	mu.Lock()
	defer mu.Unlock()
	if forever {
		return true
	}
	return !until.IsZero() && time.Now().Before(until)
}

// Grant allows automatic learning without asking, for d. d <= 0 means for the
// rest of this run (until Revoke or process exit) rather than a specific
// duration — a caller that wants "always" for this session passes 0, not an
// arbitrarily large duration that would be wrong to display.
func Grant(d time.Duration) {
	mu.Lock()
	defer mu.Unlock()
	if d <= 0 {
		forever = true
		until = time.Time{}
		return
	}
	forever = false
	until = time.Now().Add(d)
}

// Revoke withdraws any standing grant immediately. The next chat exchange
// goes back to asking before it learns.
func Revoke() {
	mu.Lock()
	defer mu.Unlock()
	forever = false
	until = time.Time{}
}

// Remaining reports how long the current grant has left. It returns 0 when
// there is no grant, and a duration you should not treat as ever expiring
// when the grant is for the rest of the run — callers that only want to know
// "am I allowed" should use Allowed instead; this exists for a UI that wants
// to show a countdown.
func Remaining() time.Duration {
	mu.Lock()
	defer mu.Unlock()
	if forever {
		return time.Duration(1<<63 - 1) // effectively "no expiry" for display purposes
	}
	if until.IsZero() {
		return 0
	}
	if d := time.Until(until); d > 0 {
		return d
	}
	return 0
}
