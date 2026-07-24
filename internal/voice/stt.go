package voice

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Speech-to-text via whisper.cpp. The bundled `whisper-cli` transcribes a
// 16 kHz mono WAV; we ask it for plain text (no timestamps, no progress) and
// return the cleaned transcript.

// Transcribe converts a 16 kHz mono WAV file to text.
func (c *Config) Transcribe(ctx context.Context, wavPath string) (string, error) {
	if !c.STTAvailable() {
		return "", fmt.Errorf("speech-to-text unavailable: bundle whisper.cpp + a model (see resources/voice)")
	}
	// -nt: no timestamps · -np: no progress prints · -otxt off (we read stdout).
	args := []string{"-m", c.WhisperModel, "-f", wavPath, "-nt", "-np", "-l", "auto"}
	out, err := c.run(ctx, c.WhisperBin, args, nil)
	if err != nil {
		return "", fmt.Errorf("whisper: %w", err)
	}
	return cleanTranscript(string(out)), nil
}

// Listen captures up to maxDur of microphone audio and transcribes it — the
// whole push-to-talk turn. The temporary WAV is removed afterwards.
func (c *Config) Listen(ctx context.Context, maxDur time.Duration) (string, error) {
	if !c.CanListen() {
		return "", fmt.Errorf("cannot listen: need whisper (STT) and ffmpeg (mic)")
	}
	tmp, err := os.CreateTemp("", "brain-listen-*.wav")
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	if err := c.Record(ctx, tmp.Name(), maxDur); err != nil {
		return "", err
	}
	return c.Transcribe(ctx, tmp.Name())
}

// cleanTranscript strips whisper's stray bracketed markers and blank lines and
// collapses the transcript to a single trimmed block.
func cleanTranscript(s string) string {
	var lines []string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		// Drop non-speech annotations like "[BLANK_AUDIO]" or "(silence)".
		if (strings.HasPrefix(ln, "[") && strings.HasSuffix(ln, "]")) ||
			(strings.HasPrefix(ln, "(") && strings.HasSuffix(ln, ")")) {
			continue
		}
		lines = append(lines, ln)
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}

// wavExt is the extension we standardise mic capture on.
func wavExt(path string) string {
	if filepath.Ext(path) == "" {
		return path + ".wav"
	}
	return path
}
