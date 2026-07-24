package voice

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"time"
)

// Microphone capture via ffmpeg. whisper.cpp wants 16 kHz mono PCM, so that is
// exactly what we ask ffmpeg to produce, keeping the temporary WAV small and the
// transcription fast.

// Record captures up to dur of microphone audio into a 16 kHz mono WAV.
func (c *Config) Record(ctx context.Context, dst string, dur time.Duration) error {
	if c.FFmpeg == "" {
		return fmt.Errorf("microphone capture needs ffmpeg")
	}
	dst = wavExt(dst)
	secs := int(dur.Seconds())
	if secs <= 0 {
		secs = 15
	}

	input, device := c.micInput()
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-f", input, "-i", device,
		"-t", strconv.Itoa(secs),
		"-ac", "1", "-ar", "16000",
		"-y", dst,
	}
	if _, err := c.run(ctx, c.FFmpeg, args, nil); err != nil {
		return fmt.Errorf("ffmpeg mic capture: %w", err)
	}
	return nil
}

// micInput returns the ffmpeg input format and device string for the current OS.
// On macOS avfoundation, ":<n>" selects audio device n with no video; the device
// index is BRAIN_MIC_DEVICE or the default mic discovered from ffmpeg.
func (c *Config) micInput() (format, device string) {
	switch runtime.GOOS {
	case "darwin":
		idx := c.MicDevice
		if idx == "" {
			idx = defaultAVFoundationMic(c.FFmpeg)
		}
		if idx == "" {
			idx = "0"
		}
		return "avfoundation", ":" + idx
	case "linux":
		dev := c.MicDevice
		if dev == "" {
			dev = "default"
		}
		return "alsa", dev
	default:
		dev := c.MicDevice
		if dev == "" {
			dev = "default"
		}
		return "dshow", "audio=" + dev
	}
}

var avAudioLine = regexp.MustCompile(`\[AVFoundation[^\]]*\]\s*\[(\d+)\]\s+(.*)`)

// defaultAVFoundationMic parses ffmpeg's device list for the first audio device.
// ffmpeg prints two numbered sections (video, then audio); we take the first
// index that appears after the "audio devices" header.
func defaultAVFoundationMic(ffmpeg string) string {
	out, _ := exec.Command(ffmpeg, "-f", "avfoundation", "-list_devices", "true", "-i", "").CombinedOutput()
	inAudio := false
	for _, line := range splitLines(string(out)) {
		if contains(line, "AVFoundation audio devices") {
			inAudio = true
			continue
		}
		if contains(line, "AVFoundation video devices") {
			inAudio = false
			continue
		}
		if inAudio {
			if m := avAudioLine.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
		} else if r != '\r' {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
