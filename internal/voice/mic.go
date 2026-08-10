package voice

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
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

// silenceFloor is the peak sample below which a capture is treated as dead air.
// A microphone denied by the OS does not error — it yields a stream of zeros, so
// even a whisper-quiet room clears this bar by a wide margin.
const silenceFloor = 100

// HasSignal reports whether a captured WAV contains any audio at all. It exists
// because the interesting failure is silent: when macOS has not granted mic
// access, AVFoundation feeds ffmpeg zeros rather than failing, ffmpeg exits 0
// with a perfectly valid WAV, and whisper hallucinates a filler word ("you",
// "Thank you.") out of the silence — which then lands in the user's input box as
// if they had said it. Checking the samples is the only honest signal we get.
func HasSignal(wavPath string) (bool, error) {
	b, err := os.ReadFile(wavPath)
	if err != nil {
		return false, err
	}
	pcm := pcmData(b)
	for i := 0; i+1 < len(pcm); i += 2 {
		s := int16(binary.LittleEndian.Uint16(pcm[i : i+2]))
		if s < 0 {
			s = -s
		}
		if s > silenceFloor {
			return true, nil
		}
	}
	return false, nil
}

// pcmData returns the contents of a RIFF/WAVE "data" chunk, or nil. We write
// these files ourselves (16 kHz mono s16le), so this only needs to walk the
// chunk list far enough to find the samples.
func pcmData(b []byte) []byte {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil
	}
	for p := 12; p+8 <= len(b); {
		size := int(binary.LittleEndian.Uint32(b[p+4 : p+8]))
		body := p + 8
		if string(b[p:p+4]) == "data" {
			if end := body + size; size > 0 && end <= len(b) {
				return b[body:end]
			}
			return b[body:]
		}
		p = body + size + size%2 // chunks are word-aligned
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
