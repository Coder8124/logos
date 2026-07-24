package voice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner records the commands it is asked to run and returns canned output,
// so the whole voice pipeline is testable without whisper, piper, or a mic.
type call struct {
	name  string
	args  []string
	stdin string
}

func recorder(stdout string) (*[]call, runner) {
	var calls []call
	return &calls, func(_ context.Context, name string, args []string, stdin []byte) ([]byte, error) {
		calls = append(calls, call{name, args, string(stdin)})
		return []byte(stdout), nil
	}
}

func TestTranscribeBuildsWhisperCommandAndCleans(t *testing.T) {
	calls, run := recorder("[BLANK_AUDIO]\n  Hello there, general.  \n(silence)\n")
	c := &Config{WhisperBin: "/bin/whisper-cli", WhisperModel: "/m/model.bin", run: run}

	got, err := c.Transcribe(context.Background(), "/tmp/a.wav")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hello there, general." {
		t.Errorf("transcript = %q, want cleaned single line", got)
	}
	if len(*calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(*calls))
	}
	args := strings.Join((*calls)[0].args, " ")
	if !strings.Contains(args, "-m /m/model.bin") || !strings.Contains(args, "-f /tmp/a.wav") {
		t.Errorf("whisper args missing model/file: %q", args)
	}
}

func TestTranscribeUnavailable(t *testing.T) {
	c := &Config{run: execRun} // no whisper bin/model
	if _, err := c.Transcribe(context.Background(), "x.wav"); err == nil {
		t.Error("transcribe should fail when STT is unavailable")
	}
}

func TestSpeakPrefersPiperThenSay(t *testing.T) {
	// Piper present + player → uses piper, then plays.
	calls, run := recorder("")
	c := &Config{PiperBin: "/b/piper", PiperVoice: "/v/voice.onnx", Player: "/b/afplay", run: run}
	if err := c.Speak(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[0].name != "/b/piper" || (*calls)[1].name != "/b/afplay" {
		t.Fatalf("expected piper then afplay, got %+v", *calls)
	}
	if (*calls)[0].stdin != "hi" {
		t.Errorf("piper should receive text on stdin, got %q", (*calls)[0].stdin)
	}
	pargs := strings.Join((*calls)[0].args, " ")
	if !strings.Contains(pargs, "--model /v/voice.onnx") || !strings.Contains(pargs, "--output_file") {
		t.Errorf("piper args wrong: %q", pargs)
	}

	// No piper → OS say.
	calls2, run2 := recorder("")
	c2 := &Config{Say: "/usr/bin/say", run: run2}
	if err := c2.Speak(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if len(*calls2) != 1 || (*calls2)[0].name != "/usr/bin/say" || (*calls2)[0].args[0] != "hello" {
		t.Fatalf("expected say fallback, got %+v", *calls2)
	}
}

func TestSpeakStreamSpeaksBySentence(t *testing.T) {
	calls, run := recorder("")
	c := &Config{Say: "/usr/bin/say", run: run}
	s := c.NewSpeakStream(context.Background())
	s.Write("Hello there. How ")
	s.Write("are you")
	s.Write(" today? Bye")
	// So far spoken: "Hello there." and "How are you today?"; "Bye" still buffered.
	if len(*calls) != 2 {
		t.Fatalf("want 2 sentences spoken mid-stream, got %d: %+v", len(*calls), *calls)
	}
	s.Flush()
	if len(*calls) != 3 || (*calls)[2].args[0] != "Bye" {
		t.Errorf("flush should speak the tail 'Bye', got %+v", *calls)
	}
}

func TestCapabilityChecks(t *testing.T) {
	full := &Config{WhisperBin: "w", WhisperModel: "m", PiperBin: "p", PiperVoice: "v", Player: "a", FFmpeg: "f"}
	if !full.STTAvailable() || !full.TTSAvailable() || !full.CanListen() || !full.CanSpeak() {
		t.Error("a fully-resolved config should have every capability")
	}
	sayOnly := &Config{Say: "/usr/bin/say"}
	if sayOnly.STTAvailable() || !sayOnly.TTSAvailable() || !sayOnly.CanSpeak() {
		t.Error("say-only should speak but not transcribe")
	}
	if sayOnly.CanListen() {
		t.Error("say-only cannot listen")
	}
}

func TestResolveFromBundledResources(t *testing.T) {
	res := t.TempDir()
	// Drop a fake bundled whisper binary + model and a piper voice.
	mk := func(rel string, exec bool) {
		p := filepath.Join(res, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		mode := os.FileMode(0o644)
		if exec {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte("x"), mode); err != nil {
			t.Fatal(err)
		}
	}
	mk("voice/whisper/whisper-cli", true)
	mk("voice/whisper/ggml-base.en.bin", false)
	mk("voice/piper/en_US-lessac-medium.onnx", false)

	t.Setenv("BRAIN_RESOURCES", res)
	// Clear any env overrides that could shadow the bundle.
	for _, e := range []string{"BRAIN_WHISPER_BIN", "BRAIN_WHISPER_MODEL", "BRAIN_PIPER_BIN", "BRAIN_PIPER_VOICE"} {
		t.Setenv(e, "")
	}

	if got := resolveBin("BRAIN_WHISPER_BIN", res, "whisper", []string{"whisper-cli"}); got == "" {
		t.Error("bundled whisper-cli should resolve")
	}
	if got := resolveFile("BRAIN_WHISPER_MODEL", res, "whisper", []string{"model.bin"}); !strings.HasSuffix(got, "ggml-base.en.bin") {
		t.Errorf("should fall back to the .bin present in the dir, got %q", got)
	}
	if got := resolveFile("BRAIN_PIPER_VOICE", res, "piper", []string{"voice.onnx"}); !strings.HasSuffix(got, ".onnx") {
		t.Errorf("should find the bundled .onnx voice, got %q", got)
	}
}

func TestStatusReflectsAvailability(t *testing.T) {
	c := &Config{Say: "/usr/bin/say", FFmpeg: "/usr/bin/ffmpeg"}
	lines := strings.Join(c.Status(), "\n")
	if !strings.Contains(lines, "text-to-speech") || !strings.Contains(lines, "not available") {
		t.Errorf("status should show TTS available and STT not, got:\n%s", lines)
	}
}
