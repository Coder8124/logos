#!/usr/bin/env bash
# Fetch the bundled voice engines (whisper.cpp for STT, Piper for TTS) and their
# models into resources/voice/. Run once at build/package time. Idempotent: it
# skips anything already present. Nothing here is committed to git.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VOICE="$ROOT/resources/voice"
WHISPER_DIR="$VOICE/whisper"
PIPER_DIR="$VOICE/piper"

# Which models to bundle (override via env before running).
WHISPER_MODEL="${WHISPER_MODEL:-base.en}"        # tiny.en | base.en | small.en
PIPER_VOICE="${PIPER_VOICE:-en_US-lessac-medium}" # see huggingface.co/rhasspy/piper-voices

OS="$(uname -s)"; ARCH="$(uname -m)"
mkdir -p "$WHISPER_DIR" "$PIPER_DIR"

say() { printf '\033[1m·\033[0m %s\n' "$*"; }

# ----- whisper.cpp (speech-to-text) -----
fetch_whisper() {
  if [ -x "$WHISPER_DIR/whisper-cli" ] || [ -x "$WHISPER_DIR/main" ]; then
    say "whisper binary already present — skipping build"
  else
    say "building whisper.cpp from source"
    local tmp; tmp="$(mktemp -d)"
    git clone --depth 1 https://github.com/ggml-org/whisper.cpp "$tmp/whisper.cpp"
    cmake -S "$tmp/whisper.cpp" -B "$tmp/whisper.cpp/build" -DCMAKE_BUILD_TYPE=Release >/dev/null
    cmake --build "$tmp/whisper.cpp/build" --config Release -j --target whisper-cli >/dev/null
    # binary path varies by version; grab whichever exists
    local bin
    bin="$(find "$tmp/whisper.cpp/build" -name whisper-cli -type f | head -1)"
    [ -z "$bin" ] && bin="$(find "$tmp/whisper.cpp/build" -name main -type f | head -1)"
    cp "$bin" "$WHISPER_DIR/whisper-cli"
    chmod +x "$WHISPER_DIR/whisper-cli"
    rm -rf "$tmp"
  fi

  local model="$WHISPER_DIR/ggml-${WHISPER_MODEL}.bin"
  if [ -f "$model" ]; then
    say "whisper model ggml-${WHISPER_MODEL}.bin already present"
  else
    say "downloading whisper model ggml-${WHISPER_MODEL}.bin"
    curl -L --fail -o "$model" \
      "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-${WHISPER_MODEL}.bin"
  fi
}

# ----- piper (text-to-speech) -----
piper_asset() {
  case "$OS-$ARCH" in
    Darwin-arm64)  echo "piper_macos_aarch64.tar.gz" ;;
    Darwin-x86_64) echo "piper_macos_x64.tar.gz" ;;
    Linux-x86_64)  echo "piper_linux_x86_64.tar.gz" ;;
    Linux-aarch64) echo "piper_linux_aarch64.tar.gz" ;;
    *) echo "" ;;
  esac
}

fetch_piper() {
  if [ -x "$PIPER_DIR/piper" ]; then
    say "piper binary already present — skipping"
  else
    local asset; asset="$(piper_asset)"
    if [ -z "$asset" ]; then
      say "no prebuilt piper for $OS-$ARCH — install piper manually into $PIPER_DIR (TTS falls back to the OS voice)"
    else
      say "downloading piper ($asset)"
      local tmp; tmp="$(mktemp -d)"
      curl -L --fail -o "$tmp/piper.tgz" \
        "https://github.com/rhasspy/piper/releases/latest/download/$asset"
      tar -xzf "$tmp/piper.tgz" -C "$tmp"
      # the tarball extracts a piper/ dir with the binary and its shared libs
      cp -R "$tmp/piper/." "$PIPER_DIR/"
      chmod +x "$PIPER_DIR/piper"
      rm -rf "$tmp"
    fi
  fi

  local onnx="$PIPER_DIR/${PIPER_VOICE}.onnx"
  if [ -f "$onnx" ]; then
    say "piper voice ${PIPER_VOICE} already present"
  else
    say "downloading piper voice ${PIPER_VOICE}"
    # voices are laid out as <lang>/<region>/<name>/<quality>/<file> on HF
    local lang region name quality base
    lang="${PIPER_VOICE%%_*}"                 # en
    region="$(echo "$PIPER_VOICE" | cut -d_ -f2 | cut -d- -f1)"  # US
    name="$(echo "$PIPER_VOICE" | cut -d- -f2)"                  # lessac
    quality="$(echo "$PIPER_VOICE" | cut -d- -f3)"               # medium
    base="https://huggingface.co/rhasspy/piper-voices/resolve/main/${lang}/${lang}_${region}/${name}/${quality}"
    curl -L --fail -o "$onnx"        "${base}/${PIPER_VOICE}.onnx"
    curl -L --fail -o "${onnx}.json" "${base}/${PIPER_VOICE}.onnx.json"
  fi
}

fetch_whisper
fetch_piper
say "done. Bundled voice engines are in $VOICE"
say "verify with: brain doctor"
