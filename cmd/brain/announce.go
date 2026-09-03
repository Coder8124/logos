package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Coder8124/brain/internal/agentprompt"
	"github.com/Coder8124/brain/internal/announce"
)

// `brain announce [on|quiet|off]` — how loudly Logos reports its own work.
//
// This exists because both failure modes are real. Silent, Logos looks broken:
// a memory is stored during someone else's refactor and nothing says so, and a
// user concludes the feature does not work. Loud on every call, it becomes
// chrome people learn to skip — and then the one message that mattered goes
// past unread with the rest.
func runAnnounce(args []string) error {
	vault := vaultPath()
	if len(args) == 0 || args[0] == "status" {
		l := announce.Setting(vault)
		fmt.Printf("announcements: %s\n", l)
		if v := os.Getenv(announce.Env); strings.TrimSpace(v) != "" {
			fmt.Printf("  set by %s=%s in the environment, which overrides the stored setting.\n", announce.Env, v)
		}
		fmt.Println()
		fmt.Println("  on     a marked receipt when Logos stores, recalls or checkpoints (default)")
		fmt.Println("  quiet  the same facts, without the marker")
		fmt.Println("  off    nothing extra — the agent is still told, you are not")
		fmt.Println()
		fmt.Println("  brain announce quiet")
		return nil
	}

	var l announce.Level
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "on", "full":
		l = announce.On
	case "quiet", "plain":
		l = announce.Quiet
	case "off", "silent":
		l = announce.Off
	default:
		return fmt.Errorf("usage: brain announce [on|quiet|off|status]")
	}
	if err := announce.Store(vault, l); err != nil {
		return err
	}
	// The confirmation deliberately ignores the setting it just wrote: a
	// command whose whole job is to change what gets said should say what it
	// did, including when it has just turned everything else off.
	fmt.Printf("announcements: %s\n", l)
	if l == announce.Off {
		fmt.Println("  Logos will keep working and stop mentioning it. `brain activity` still has the record.")
	}
	if v := os.Getenv(announce.Env); strings.TrimSpace(v) != "" {
		fmt.Printf("  note: %s=%s is set in this environment and overrides what was just stored.\n", announce.Env, v)
	}
	return nil
}

// `brain prompt` prints the instructions agents are given. Two audiences: a
// person deciding whether to trust this thing with their vault, and a person
// pasting it into a CLAUDE.md for a host that ignores the MCP `instructions`
// field. Both want the same bytes the agent gets, which is why this reads the
// embedded copy rather than a file on disk that may have been edited since.
func runPrompt(args []string) error {
	fmt.Print(agentprompt.Text())
	return nil
}
