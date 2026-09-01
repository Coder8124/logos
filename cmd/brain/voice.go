package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/agent"
	"github.com/Coder8124/brain/internal/voice"
)

// Voice: talk to the assistant and hear it back, all locally. `say` speaks text,
// `listen` transcribes a mic turn, and `voice` is a hands-on-keyboard voice Q&A
// loop over the vault.

func runSay(text string) error {
	v := voice.New()
	if !v.CanSpeak() {
		return fmt.Errorf("text-to-speech unavailable — bundle Piper (see resources/voice) or run on macOS")
	}
	return v.Speak(context.Background(), text)
}

func runListen(seconds int) error {
	v := voice.New()
	if !v.CanListen() {
		return fmt.Errorf("cannot listen — bundle whisper.cpp + a model (see resources/voice); ffmpeg must be installed")
	}
	fmt.Printf("· listening for %ds — speak now…\n", seconds)
	text, err := v.Listen(context.Background(), time.Duration(seconds)*time.Second)
	if err != nil {
		return err
	}
	if text == "" {
		fmt.Println("(heard nothing)")
		return nil
	}
	fmt.Printf("\n%s\n", text)
	return nil
}

// runVoiceChat is a voice conversation with the vault: press Enter to talk, hear
// the answer, repeat. Explicit turn-taking keeps it simple and Ctrl-C-free.
func runVoiceChat(seconds int) error {
	v := voice.New()
	if !v.CanListen() {
		return fmt.Errorf("voice chat needs speech-to-text — bundle whisper.cpp + a model (see resources/voice)")
	}
	speak := v.CanSpeak()
	if !speak {
		fmt.Println("· note: no text-to-speech available; answers will be shown, not spoken")
	}
	ctx := context.Background()
	fmt.Println("voice chat over your vault. Press Enter to talk, or type q then Enter to quit.")
	in := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("\n▶ Enter to talk (q to quit): ")
		if !in.Scan() {
			return nil
		}
		if strings.TrimSpace(strings.ToLower(in.Text())) == "q" {
			return nil
		}

		fmt.Printf("· listening %ds…\n", seconds)
		question, err := v.Listen(ctx, time.Duration(seconds)*time.Second)
		if err != nil {
			return err
		}
		if question == "" {
			fmt.Println("(heard nothing — try again)")
			continue
		}
		fmt.Printf("you: %s\n", question)

		answer, err := voiceAnswer(question)
		if err != nil {
			return err
		}
		fmt.Printf("brain: %s\n", answer)
		if speak {
			v.Speak(ctx, answer)
		}
	}
}

// voiceAnswer runs a vault question through the same retrieval path as `brain
// ask`, returning just the answer text.
func voiceAnswer(question string) (string, error) {
	ix, err := openIndex()
	if err != nil {
		return "", err
	}
	defer ix.Close()

	rt, err := openRouter()
	if err != nil {
		return "", err
	}

	// Answer with the same grounding the agent uses — vault retrieval + persistent
	// memory + what's on your plate — so the presence recalls what it has learned
	// about you, not only what happens to be written in the vault.
	answer, err := agent.Reply(ix.DB, ix, rt, &agent.Conversation{}, question, func(string) {})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(answer), nil
}
