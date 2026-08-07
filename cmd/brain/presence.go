package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pragun/brain/internal/flavor"
	"github.com/pragun/brain/internal/presence"
	"github.com/pragun/brain/internal/secretary"
	"github.com/pragun/brain/internal/voice"
)

// runName sets or shows the assistant's name — how you address it and, with a
// wake-word model present, the word that wakes it.
func runName(arg string) error {
	vault := vaultPath()
	cfg, err := flavor.Load(vault)
	if err != nil {
		return err
	}
	arg = strings.TrimSpace(arg)
	if arg == "" {
		if cfg.Name == "" {
			fmt.Println("no name set — `brain name <name>` gives it one (and a wake word).")
		} else {
			fmt.Printf("I answer to \"%s\".\n", cfg.Name)
		}
		return nil
	}
	cfg.Name = arg
	if err := cfg.Save(vault); err != nil {
		return err
	}
	fmt.Printf("· done — I'll answer to \"%s\".\n", arg)
	return nil
}

// runPresence is the ambient secretary: it opens with the brief, answers from your
// memory, and raises the occasional evidence-backed nudge — bounded by the law
// that it proposes but never decides, and never acts outward without the gate.
func runPresence(forceWake bool) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	cfg, err := flavor.Load(ix.Vault)
	if err != nil {
		return err
	}
	if err := presence.Init(ix.DB); err != nil {
		return err
	}
	name := cfg.Name
	prefs := presencePrefs(cfg)
	wakeEnabled := cfg.Presence.WakeWord

	v := voice.New()
	canSpeak := v.CanSpeak()
	canListen := v.CanListen()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	greet(ctx, ix.DB, v, name, canSpeak)

	wakeMode := (wakeEnabled || forceWake) && canListen && name != ""
	if forceWake && name == "" {
		fmt.Println("· --wake needs a name first: `brain name <name>`")
	}
	switch {
	case wakeMode:
		fmt.Printf("· listening for \"%s\" (ctrl-C to stop)\n", name)
	case canListen:
		fmt.Println("· press Enter to talk, or type your question (q to quit)")
	default:
		fmt.Println("· no microphone — type your question (q to quit)")
	}

	in := bufio.NewScanner(os.Stdin)
	for {
		if ctx.Err() != nil {
			return nil
		}

		// While idle, raise at most one due interjection (cooldown-spaced).
		if it, _ := presence.Next(ix.DB, time.Now(), prefs, false); it != nil {
			sayInterjection(ctx, v, name, it, canSpeak)
		}

		question, quit := awaitTurn(ctx, in, v, name, wakeMode, canListen, canSpeak)
		if quit {
			return nil
		}
		if strings.TrimSpace(question) == "" {
			continue
		}
		fmt.Printf("you: %s\n", question)

		answer, err := voiceAnswer(question)
		if err != nil {
			fmt.Fprintln(os.Stderr, "· error:", err)
			continue
		}
		fmt.Printf("%s: %s\n", speakerName(name), answer)
		if canSpeak {
			v.Speak(ctx, answer)
		}
	}
}

// awaitTurn blocks until the user starts a turn — by wake word, by Enter, or by
// typing — and returns their question. quit is true when they asked to stop.
func awaitTurn(ctx context.Context, in *bufio.Scanner, v *voice.Config, name string, wakeMode, canListen, canSpeak bool) (string, bool) {
	if wakeMode {
		heard, err := v.WakeHeard(ctx, name, 3*time.Second)
		if err != nil || ctx.Err() != nil {
			return "", true
		}
		if !heard {
			return "", false // no wake this window; caller loops (and re-checks nudges)
		}
		ack(ctx, v, name, canSpeak)
		q, _ := v.Listen(ctx, 15*time.Second)
		return q, false
	}

	fmt.Print("\n▶ ")
	if !in.Scan() {
		return "", true
	}
	line := strings.TrimSpace(in.Text())
	if line == "q" {
		return "", true
	}
	// Empty line means "let me speak"; typed text is a question directly.
	if line == "" && canListen {
		q, _ := v.Listen(ctx, 15*time.Second)
		return q, false
	}
	return line, false
}

func presencePrefs(cfg *flavor.Config) presence.Prefs {
	p := cfg.Presence.WithDefaults()
	pr := presence.Prefs{
		Interjections: true, // running `brain presence` is opting into it
		LeadMinutes:   p.MeetingLeadMinutes,
		MinGapMinutes: p.MinGapMinutes,
	}
	if len(p.QuietHours) == 2 {
		pr.QuietStart, pr.QuietEnd = p.QuietHours[0], p.QuietHours[1]
	}
	return pr
}

func speakerName(name string) string {
	if name == "" {
		return "brain"
	}
	return name
}

// greet opens with the day's greeting and the single most pressing thing, no more.
func greet(ctx context.Context, db *sql.DB, v *voice.Config, name string, canSpeak bool) {
	b, err := secretary.Compose(db, time.Now())
	if err != nil {
		return
	}
	line := b.Greeting
	switch {
	case len(b.Upcoming) > 0:
		line += fmt.Sprintf(" %s is at %s.", b.Upcoming[0].Title, b.Upcoming[0].At)
	case len(b.Loops) > 0:
		line += fmt.Sprintf(" Top of your list: %s.", b.Loops[0].Text)
	}
	fmt.Printf("%s: %s\n", speakerName(name), line)
	if canSpeak {
		v.Speak(ctx, line)
	}
}

func sayInterjection(ctx context.Context, v *voice.Config, name string, it *presence.Interjection, canSpeak bool) {
	fmt.Printf("%s: %s  (%s)\n", speakerName(name), it.Text, it.Detail)
	if canSpeak {
		v.Speak(ctx, it.Text)
	}
}

func ack(ctx context.Context, v *voice.Config, name string, canSpeak bool) {
	fmt.Printf("%s: mm?\n", speakerName(name))
	if canSpeak {
		v.Speak(ctx, "Mm?")
	}
}
