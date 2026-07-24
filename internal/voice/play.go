package voice

import (
	"context"
	"fmt"
	"path/filepath"
)

// Playback of a WAV through whatever player resolved (afplay on macOS, else
// ffplay or aplay). Kept separate so synthesis and playback can be used apart.

// Play plays a WAV file, blocking until it finishes.
func (c *Config) Play(ctx context.Context, wavPath string) error {
	if c.Player == "" {
		return fmt.Errorf("no audio player found (afplay/ffplay/aplay)")
	}
	args := playerArgs(c.Player, wavPath)
	_, err := c.run(ctx, c.Player, args, nil)
	return err
}

// playerArgs adapts flags to the specific player; ffplay needs coaxing to exit
// on its own and stay silent.
func playerArgs(player, wav string) []string {
	switch filepath.Base(player) {
	case "ffplay":
		return []string{"-autoexit", "-nodisp", "-loglevel", "error", wav}
	default: // afplay, aplay
		return []string{wav}
	}
}
