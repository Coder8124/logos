# Bundled voice engines

brain speaks and listens entirely on-device. The engines are self-contained
native binaries plus their model files, shipped in this directory and invoked as
subprocesses — no cgo in the Go core, no cloud, no per-word API.

## Layout

```
resources/voice/
  whisper/
    whisper-cli            # whisper.cpp binary (speech-to-text)
    ggml-base.en.bin       # a whisper GGML model
  piper/
    piper                  # piper binary (text-to-speech)
    <voice>.onnx           # a piper voice model
    <voice>.onnx.json      # its config (must sit beside the .onnx)
```

Only `.gitkeep` and this README are committed. The binaries and models are large,
so they are **fetched at build/package time**, not stored in git:

```sh
scripts/fetch-voice.sh
```

## How brain finds them

At runtime `internal/voice` resolves each tool in this order:

1. an environment override — `BRAIN_WHISPER_BIN`, `BRAIN_WHISPER_MODEL`,
   `BRAIN_PIPER_BIN`, `BRAIN_PIPER_VOICE`, `BRAIN_MIC_DEVICE`
2. this bundled directory (or, in a packaged macOS app,
   `Contents/Resources/voice/…`)
3. a plain `PATH` lookup, for developers who installed `whisper-cli` / `piper`
   themselves

Text-to-speech additionally falls back to the OS voice (macOS `say`), so the
assistant can talk even before Piper is bundled. Microphone capture uses
`ffmpeg`; playback uses `afplay` (macOS), `ffplay`, or `aplay`.

Run `brain doctor` to see exactly what resolved.

## Engines

- **Speech-to-text:** [whisper.cpp](https://github.com/ggml-org/whisper.cpp).
  Swap in any GGML model (`ggml-tiny.en.bin` for speed, `ggml-base.en.bin` for
  the default balance, `ggml-small.en.bin` for accuracy). Moonshine can be
  dropped in by pointing `BRAIN_WHISPER_BIN` at a compatible CLI.
- **Text-to-speech:** [Piper](https://github.com/rhasspy/piper). Any
  [piper voice](https://huggingface.co/rhasspy/piper-voices) works; keep the
  `.onnx` and `.onnx.json` together.
