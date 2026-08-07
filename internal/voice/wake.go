package voice

import (
	"context"
	"strings"
	"time"
)

// Wake-word listening. The name you give the assistant is its wake word.
//
// There is no dedicated low-power wake-word engine here yet: this transcribes a
// short rolling window with whisper and checks whether the name was spoken. It is
// honest but CPU-warm — a purpose-built engine (openWakeWord / porcupine-class)
// can drop in behind WakeHeard later via the same env-override resolution the
// other voice tools use, without changing callers.
//
// Nothing is retained: each window is transcribed and discarded, and transcription
// only ever runs on the short buffer, so the "always listening" surface stays as
// small as it can be while remaining on-device.

// WakeAvailable reports whether wake-word listening is possible — it needs the
// same transcription toolchain a normal turn does.
func (c *Config) WakeAvailable() bool { return c.CanListen() }

// WakeHeard records one short window and reports whether name was spoken in it.
// Callers loop it so they can do other work (like checking for a due interjection)
// between windows, rather than blocking on a single long listen.
func (c *Config) WakeHeard(ctx context.Context, name string, window time.Duration) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, nil
	}
	text, err := c.Listen(ctx, window)
	if err != nil {
		return false, err
	}
	return WakeMatch(text, name), nil
}

// WakeMatch reports whether a heard phrase contains the wake name. Deliberately
// forgiving — whisper punctuates and cases freely, so it matches on a lowered,
// punctuation-stripped word check rather than an exact string.
func WakeMatch(heard, name string) bool {
	h := normalizeWake(heard)
	n := normalizeWake(name)
	if n == "" {
		return false
	}
	return strings.Contains(" "+h+" ", " "+n+" ")
}

func normalizeWake(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
