package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// People type `<command> --help` before they try a command. Most commands
// ignored the flag and ran: `update --help` replaced the binary, `note --help`
// wrote a note reading "--help", `index --help` rebuilt the index. Every
// command, and each subcommand the help names, has to print its usage and
// touch nothing.
func TestHelpOnEveryCommandPrintsItsUsageAndTouchesNothing(t *testing.T) {
	var all strings.Builder
	helpAll(&all)
	seen := map[string]bool{}
	var commands [][]string
	for _, m := range regexp.MustCompile(`(?m)^    logos ([a-z-]+)(?: ([a-z]+)\b)?`).FindAllStringSubmatch(all.String(), -1) {
		for _, c := range [][]string{{m[1]}, {m[1], m[2]}} {
			if m[1] == "help" || m[1] == "version" || c[len(c)-1] == "" || seen[strings.Join(c, " ")] {
				continue
			}
			seen[strings.Join(c, " ")] = true
			commands = append(commands, c)
		}
	}
	if len(commands) < 30 {
		t.Fatalf("read only %d commands out of the help: %v", len(commands), commands)
	}

	for _, command := range commands {
		for _, flag := range []string{"--help", "-h"} {
			name := strings.Join(command, " ") + " " + flag
			t.Run(name, func(t *testing.T) {
				home, tmp := t.TempDir(), t.TempDir()
				vault := filepath.Join(t.TempDir(), "vault")
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0])
				cmd.Env = append(os.Environ(),
					runMainEnv+"="+strings.Join(append(command, flag), "\x1f"),
					"HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
					"TMPDIR="+tmp, "LOGOS_VAULT="+vault, "LOGOS_EMBED=off", "LOGOS_RUNTIME=http://192.0.2.1:1/v1")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("exited with %v:\n%s", err, out)
				}
				if !strings.Contains(string(out), "logos "+command[0]) {
					t.Errorf("did not print the usage of %s:\n%s", command[0], out)
				}
				for _, dir := range []string{home, tmp} {
					if entries, _ := os.ReadDir(dir); len(entries) > 0 {
						t.Errorf("wrote into %s: %v\n%s", dir, entries, out)
					}
				}
				if _, err := os.Stat(vault); err == nil {
					t.Errorf("created the vault:\n%s", out)
				}
			})
		}
	}
}

// update used to look for --check and otherwise update, so `update --chek` or
// `update --dry-run` replaced the binary too.
func TestUpdateRefusesAFlagItDoesNotKnow(t *testing.T) {
	old := version
	version = "0.0.1"
	t.Cleanup(func() { version = old })
	err := updateCmd([]string{"--chek"})
	if err == nil || !strings.Contains(err.Error(), "--chek") {
		t.Fatalf("update with an unknown flag should refuse it by name, got %v", err)
	}
}
