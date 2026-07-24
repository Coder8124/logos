package voice

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Text-to-speech. Piper (bundled) is the preferred engine: a small, fast,
// fully-local neural voice. When it is not bundled we fall back to the OS voice
// (macOS `say`) so the assistant can always talk.

// Speak says text aloud, blocking until it finishes. It prefers the bundled
// Piper voice and falls back to the OS voice.
func (c *Config) Speak(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if c.PiperBin != "" && c.PiperVoice != "" && c.Player != "" {
		return c.speakPiper(ctx, text)
	}
	if c.Say != "" {
		_, err := c.run(ctx, c.Say, []string{text}, nil)
		return err
	}
	return fmt.Errorf("text-to-speech unavailable: bundle Piper (see resources/voice) or run on macOS")
}

// Synthesize writes speech for text to a WAV file without playing it — for
// saving a spoken reply or piping elsewhere.
func (c *Config) Synthesize(ctx context.Context, text, wavOut string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("nothing to synthesize")
	}
	switch {
	case c.PiperBin != "" && c.PiperVoice != "":
		_, err := c.run(ctx, c.PiperBin,
			[]string{"--model", c.PiperVoice, "--output_file", wavOut}, []byte(text))
		return err
	case c.Say != "":
		// `say -o` writes AIFF; callers that need WAV should prefer Piper. We keep
		// the extension the caller asked for and let `say` infer the container.
		_, err := c.run(ctx, c.Say, []string{"-o", wavOut, text}, nil)
		return err
	default:
		return fmt.Errorf("text-to-speech unavailable")
	}
}

// speakPiper synthesizes to a temp WAV and plays it through the resolved player.
func (c *Config) speakPiper(ctx context.Context, text string) error {
	tmp, err := os.CreateTemp("", "brain-say-*.wav")
	if err != nil {
		return err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	if _, err := c.run(ctx, c.PiperBin,
		[]string{"--model", c.PiperVoice, "--output_file", tmp.Name()}, []byte(text)); err != nil {
		return fmt.Errorf("piper: %w", err)
	}
	return c.Play(ctx, tmp.Name())
}

// SpeakStream speaks text sentence by sentence as it arrives, so a streamed
// model reply starts being spoken before it has finished generating. Feed it
// tokens; call Flush at the end to speak the tail.
type SpeakStream struct {
	c   *Config
	ctx context.Context
	buf strings.Builder
}

// NewSpeakStream starts an incremental speaker.
func (c *Config) NewSpeakStream(ctx context.Context) *SpeakStream {
	return &SpeakStream{c: c, ctx: ctx}
}

// Write buffers tokens and speaks each complete sentence as its boundary passes.
func (s *SpeakStream) Write(chunk string) {
	s.buf.WriteString(chunk)
	text := s.buf.String()
	// Speak up to the last sentence terminator; keep the remainder buffered.
	if i := lastSentenceBreak(text); i > 0 {
		spoken := strings.TrimSpace(text[:i+1])
		rest := text[i+1:]
		s.buf.Reset()
		s.buf.WriteString(rest)
		if spoken != "" {
			s.c.Speak(s.ctx, spoken)
		}
	}
}

// Flush speaks whatever remains in the buffer.
func (s *SpeakStream) Flush() {
	if rest := strings.TrimSpace(s.buf.String()); rest != "" {
		s.c.Speak(s.ctx, rest)
	}
	s.buf.Reset()
}

// lastSentenceBreak returns the index of the last sentence-ending punctuation,
// or -1. Used to speak in natural chunks rather than mid-word.
func lastSentenceBreak(s string) int {
	last := -1
	for i, r := range s {
		if r == '.' || r == '!' || r == '?' || r == '\n' {
			last = i
		}
	}
	return last
}
