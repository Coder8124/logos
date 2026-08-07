package main

import (
	"context"
	"fmt"
	"time"

	"github.com/pragun/brain/internal/voice"
)

// Speak-to-type: the same text conversation, entered by voice. It records a short
// mic turn and transcribes it locally, so the user can talk into the chat instead
// of typing. Entirely on-device (whisper.cpp) — nothing leaves the machine.

// VoiceAvailable reports whether the panel can offer speak-to-type: it needs the
// local speech-to-text toolchain (whisper.cpp + a model + ffmpeg). The frontend
// hides the mic button when this is false.
func (a *App) VoiceAvailable() bool {
	return voice.New().CanListen()
}

// VoiceInput records a mic turn and returns the transcription, to drop into the
// chat input as if typed.
func (a *App) VoiceInput() (string, error) {
	v := voice.New()
	if !v.CanListen() {
		return "", fmt.Errorf("speech-to-text isn't set up — bundle whisper.cpp and a model")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return v.Listen(ctx, 15*time.Second)
}
