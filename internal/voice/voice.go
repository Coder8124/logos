// Package voice gives the assistant ears and a mouth, locally.
//
// Speech-to-text (whisper.cpp) and text-to-speech (Piper) both run as
// self-contained native binaries the product bundles alongside their model
// files — no cgo, no cloud, no per-word API. This mirrors how the rest of the
// system treats heavy native tools (ffmpeg, the model runtimes): find the
// binary, shell out, degrade gracefully when it is absent. Your voice never
// leaves the machine.
//
// Resolution order for every binary and model, most specific first:
//  1. an explicit environment override (BRAIN_WHISPER_BIN, …)
//  2. the bundled resources directory shipped with the app
//  3. a plain PATH lookup, for developers who installed the tools themselves
//
// Text-to-speech additionally falls back to the OS voice (macOS `say`) so the
// assistant can always talk, even before the Piper voice is bundled in.
package voice

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Config holds the resolved tools. A zero value is useless; call New.
type Config struct {
	// speech-to-text (whisper.cpp)
	WhisperBin   string
	WhisperModel string

	// text-to-speech (Piper), with the OS voice as fallback
	PiperBin   string
	PiperVoice string
	Say        string // macOS `say`, resolved if present

	// audio I/O
	FFmpeg    string // mic capture
	Player    string // wav playback (afplay/ffplay/aplay)
	MicDevice string // avfoundation audio device index, "" → default

	// run executes a command; swappable in tests so nothing shells out.
	run runner
}

type runner func(ctx context.Context, name string, args []string, stdin []byte) (stdout []byte, err error)

// New resolves the voice toolchain from the environment, the bundled resources
// directory, and PATH. It never fails: an unresolved tool just leaves the
// corresponding capability unavailable, which the caller checks before using.
func New() *Config {
	res := resourcesDir()
	c := &Config{run: execRun}

	c.WhisperBin = resolveBin("BRAIN_WHISPER_BIN", res, "whisper", []string{"whisper-cli", "whisper", "main"})
	c.WhisperModel = resolveFile("BRAIN_WHISPER_MODEL", res, "whisper", []string{"model.bin", "ggml-base.en.bin", "ggml-base.bin", "ggml-tiny.en.bin"})

	c.PiperBin = resolveBin("BRAIN_PIPER_BIN", res, "piper", []string{"piper"})
	c.PiperVoice = resolveFile("BRAIN_PIPER_VOICE", res, "piper", []string{"voice.onnx", "en_US-lessac-medium.onnx", "en_US-amy-medium.onnx"})

	if runtime.GOOS == "darwin" {
		c.Say, _ = exec.LookPath("say")
	}
	c.FFmpeg, _ = exec.LookPath("ffmpeg")
	c.Player = resolvePlayer()
	c.MicDevice = os.Getenv("BRAIN_MIC_DEVICE")
	return c
}

// --- capability checks ---

// STTAvailable reports whether transcription is possible (binary + model).
func (c *Config) STTAvailable() bool { return c.WhisperBin != "" && c.WhisperModel != "" }

// TTSAvailable reports whether synthesis is possible (Piper bundled, or the OS
// voice as fallback).
func (c *Config) TTSAvailable() bool { return (c.PiperBin != "" && c.PiperVoice != "") || c.Say != "" }

// CanListen reports whether we can capture the mic and transcribe it.
func (c *Config) CanListen() bool { return c.STTAvailable() && c.FFmpeg != "" }

// CanSpeak reports whether we can produce audible speech (Piper needs a player;
// the OS `say` plays for itself).
func (c *Config) CanSpeak() bool {
	if c.PiperBin != "" && c.PiperVoice != "" && c.Player != "" {
		return true
	}
	return c.Say != ""
}

// Status returns human-readable lines for `brain doctor`.
func (c *Config) Status() []string {
	line := func(label, detail string, ok bool) string {
		mark := "not available"
		if ok {
			mark = detail
		}
		return padRight(label, 22) + mark
	}
	var out []string
	out = append(out, line("speech-to-text", "whisper · "+base(c.WhisperModel), c.STTAvailable()))
	ttsDetail := "OS voice (say)"
	if c.PiperBin != "" && c.PiperVoice != "" {
		ttsDetail = "piper · " + base(c.PiperVoice)
	}
	out = append(out, line("text-to-speech", ttsDetail, c.TTSAvailable()))
	out = append(out, line("microphone", "ffmpeg", c.FFmpeg != ""))
	out = append(out, line("playback", base(c.Player), c.Player != ""))
	return out
}

// --- resolution helpers ---

// resourcesDir is where bundled binaries and models live. Overridable, else the
// app bundle's Resources (macOS) or a resources/ dir beside the executable.
func resourcesDir() string {
	if r := os.Getenv("BRAIN_RESOURCES"); r != "" {
		return r
	}
	exe, err := os.Executable()
	if err != nil {
		return "resources"
	}
	dir := filepath.Dir(exe)
	// Wails/macOS app bundle: …/Contents/MacOS/brain → …/Contents/Resources
	if runtime.GOOS == "darwin" && filepath.Base(dir) == "MacOS" {
		return filepath.Join(filepath.Dir(dir), "Resources")
	}
	return filepath.Join(dir, "resources")
}

// voiceDirs is where bundled/installed voice assets are searched, in order: the
// app's resources dir (bundle or beside the binary) and a stable user-level
// home (~/.brain/voice). The user dir survives rebuilds and app-bundle
// repackaging, so a model dropped there is found by both the CLI and the app.
func voiceDirs(res, sub string) []string {
	dirs := []string{filepath.Join(res, "voice", sub)}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".brain", "voice", sub))
	}
	return dirs
}

// resolveBin finds an executable: env override, then the voice dirs, then PATH.
func resolveBin(env, res, sub string, names []string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	for _, dir := range voiceDirs(res, sub) {
		for _, n := range names {
			if p := filepath.Join(dir, n); isExec(p) {
				return p
			}
		}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// resolveFile finds a data file (a model): env override, then the first of the
// candidate names present in any voice dir, then any file of the right
// extension dropped in one.
func resolveFile(env, res, sub string, names []string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	// Prefer an exact, named model in the earliest dir that has one.
	for _, dir := range voiceDirs(res, sub) {
		for _, n := range names {
			if p := filepath.Join(dir, n); fileExists(p) {
				return p
			}
		}
	}
	// Last resort: any .bin (whisper) / .onnx (piper) dropped in a voice dir.
	ext := ".bin"
	if sub == "piper" {
		ext = ".onnx"
	}
	for _, dir := range voiceDirs(res, sub) {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ext) {
					return filepath.Join(dir, e.Name())
				}
			}
		}
	}
	return ""
}

func resolvePlayer() string {
	for _, p := range []string{"afplay", "ffplay", "aplay"} {
		if path, err := exec.LookPath(p); err == nil {
			return path
		}
	}
	return ""
}

// --- small fs helpers ---

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func isExec(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func base(p string) string {
	if p == "" {
		return "—"
	}
	return filepath.Base(p)
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

// execRun is the real command runner.
func execRun(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	// stderr is progress/log noise for these tools; discard it.
	if err := cmd.Run(); err != nil {
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}
