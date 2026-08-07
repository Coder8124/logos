package main

import (
	"fmt"
	"strings"

	"github.com/pragun/brain/internal/router"
)

// runThink sets or shows how much the model reasons before answering — the share
// of its token budget allowed for thinking. Reasoning models otherwise spend the
// whole budget thinking and return nothing; this is the knob that bounds it.
//
//	brain think                 show the current level
//	brain think off|low|medium|high
func runThink(arg string) error {
	vault := vaultPath()
	cfg, err := router.Load(vault)
	if err != nil {
		return err
	}

	arg = strings.TrimSpace(strings.ToLower(arg))
	if arg == "" {
		level := cfg.Think
		if level == "" {
			level = "low (default)"
		}
		fmt.Printf("thinking: %s\n", level)
		fmt.Println("levels: off · low · medium · high   (how much of the budget goes to reasoning)")
		return nil
	}

	switch arg {
	case "off", "low", "medium", "high":
	default:
		return fmt.Errorf("level must be off, low, medium, or high")
	}
	cfg.Think = arg
	if err := cfg.Save(vault); err != nil {
		return err
	}
	fmt.Printf("· thinking set to %s\n", arg)
	return nil
}
