package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Each retired spelling names its replacement, and the replacement has to reach
// the handler the old one did — otherwise the notice tells people to type a
// command that does something else, or nothing.
func TestEveryNewSpellingReachesTheHandlerTheOldOneDid(t *testing.T) {
	for old, now := range renamed {
		words := strings.Fields(strings.TrimPrefix(now, "logos "))
		got, _ := surface(words[0], words[1:])
		if got != old {
			t.Errorf("%q is said to replace `logos %s`, and reaches %q", now, old, got)
		}
	}
}

// The flag that picks the form is the surface's, not the handler's: left in,
// `show --history` hands sessions a --history it refuses, and `import --from
// transcripts status` hands ingest a project called "--from".
func TestTheFormFlagIsRemovedAndEveryOtherArgumentKept(t *testing.T) {
	for _, c := range []struct {
		cmd      string
		args     []string
		wantCmd  string
		wantArgs []string
	}{
		{"show", []string{"--history", "kestrel"}, "sessions", []string{"kestrel"}},
		{"show", []string{"--graph", "kestrel", "--hops", "3"}, "graph", []string{"kestrel", "--hops", "3"}},
		{"import", []string{"--from", "transcripts", "status"}, "ingest", []string{"status"}},
		{"import", []string{"--from", "git", "--months", "2"}, "bootstrap", []string{"--months", "2"}},
		{"import", []string{"--from", "claude-mem"}, "import", []string{"--from", "claude-mem"}},
		{"config", []string{"announce", "quiet"}, "announce", []string{"quiet"}},
		{"config", []string{"auto", "config", "index", "on"}, "auto", []string{"config", "index", "on"}},
		{"stats", []string{"kestrel"}, "insights", []string{"kestrel"}},
		{"stats", []string{"--usage", "--week"}, "usage", []string{"--week"}},
		{"memory", []string{"recall", "embeddings"}, "recall", []string{"embeddings"}},
		{"memory", []string{"add", "a fact"}, "memory", []string{"add", "a fact"}},
		{"watch", []string{"--log", "--days", "2"}, "activity", []string{"--days", "2"}},
		{"review", []string{"--candidates", "--promote", "4"}, "ingest", []string{"review", "--promote", "4"}},
		{"review", []string{"--connections"}, "dream", []string{"review"}},
		{"review", []string{"--connections", "accept", "7"}, "dream", []string{"accept", "7"}},
		{"setup", []string{"--uninstall", "--host", "cursor"}, "mcp", []string{"uninstall", "--host", "cursor"}},
		{"search", []string{"--answer", "why", "sqlite"}, "ask", []string{"why", "sqlite"}},
	} {
		cmd, args := surface(c.cmd, c.args)
		if cmd != c.wantCmd || !slices.Equal(args, c.wantArgs) {
			t.Errorf("logos %s %v reached %s %v, want %s %v", c.cmd, c.args, cmd, args, c.wantCmd, c.wantArgs)
		}
	}
}

// The notice is for a person at a terminal. A hook's stderr lands in a host's
// log, and `activity record` / `dream --auto` are what installed hooks and
// logos's own background runs type, so a notice there is noise nobody can act
// on — and one that would arrive on every prompt.
func TestTheRenameNoticeIsForPeopleNotForHooks(t *testing.T) {
	var b bytes.Buffer
	noteRenamed(&b, "sessions", nil)
	if !strings.Contains(b.String(), "logos show --history") {
		t.Errorf("an old spelling did not say what replaced it: %q", b.String())
	}

	for _, quiet := range [][]string{
		{"activity", "record", "--event", "prompt"},
		{"activity", "notice", "/tmp/x"},
		{"dream", "--auto"},
		{"note", "nothing renamed"},
	} {
		b.Reset()
		noteRenamed(&b, quiet[0], quiet[1:])
		if b.Len() > 0 {
			t.Errorf("logos %v printed a rename notice: %q", quiet, b.String())
		}
	}

	t.Setenv("LOGOS_HOOK", "1")
	b.Reset()
	noteRenamed(&b, "sessions", nil)
	if b.Len() > 0 {
		t.Errorf("a hook got a rename notice: %q", b.String())
	}
}

// Run for real: the old spelling and the new one print the same answer on
// stdout, and only the old one adds the notice, on stderr.
func TestAnOldSpellingStillRunsAndSaysOnlyOnStderrWhatReplacedIt(t *testing.T) {
	vault := t.TempDir()
	run := func(words ...string) (string, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		home := t.TempDir()
		cmd := exec.CommandContext(ctx, os.Args[0])
		cmd.Env = append(os.Environ(), runMainEnv+"="+strings.Join(words, "\x1f"),
			"HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
			"LOGOS_VAULT="+vault, "LOGOS_EMBED=off", "LOGOS_RUNTIME=off", "LOGOS_HOOK=")
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		if err := cmd.Run(); err != nil {
			t.Fatalf("logos %v: %v\n%s%s", words, err, out.String(), errOut.String())
		}
		return out.String(), errOut.String()
	}

	oldOut, oldErr := run("prompt")
	newOut, newErr := run("config", "prompt")
	if oldOut != newOut || oldOut == "" {
		t.Errorf("`logos prompt` and `logos config prompt` answered differently:\n%s\n---\n%s", oldOut, newOut)
	}
	if !strings.Contains(oldErr, "logos config prompt") {
		t.Errorf("`logos prompt` did not say what replaced it:\n%s", oldErr)
	}
	if strings.Contains(newErr, "is now") {
		t.Errorf("the new spelling announced a rename:\n%s", newErr)
	}
}

// `--help` on a form surface hands to another verb must be answered before the
// hand-off: `setup --uninstall --help` became `mcp uninstall --help`, which
// skipped the early help check because setup prints its own usage, and the
// uninstall would have run.
func TestHelpOnAHandedOffFormTouchesNothing(t *testing.T) {
	for _, words := range [][]string{
		{"setup", "--uninstall", "--help"},
		{"review", "--candidates", "--help"},
		{"review", "--connections", "--help"},
	} {
		home, vault := t.TempDir(), filepath.Join(t.TempDir(), "vault")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0])
		cmd.Env = append(os.Environ(), runMainEnv+"="+strings.Join(words, "\x1f"),
			"HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
			"LOGOS_VAULT="+vault, "LOGOS_EMBED=off", "LOGOS_RUNTIME=off")
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("logos %v: %v\n%s", words, err, out)
		}
		if !strings.Contains(string(out), "logos "+words[0]) {
			t.Errorf("logos %v did not print its usage:\n%s", words, out)
		}
		if entries, _ := os.ReadDir(home); len(entries) > 0 {
			t.Errorf("logos %v wrote into HOME: %v\n%s", words, entries, out)
		}
	}
}

// Every command the short help lists answers `--help` with something: a
// condensed command whose only help line was under its old name would print
// nothing and fall through to running.
func TestEveryListedCommandHasAHelpEntry(t *testing.T) {
	var short bytes.Buffer
	helpShort(&short)
	inList := false
	for _, line := range strings.Split(short.String(), "\n") {
		switch {
		case line == "Available Commands:":
			inList = true
			continue
		case line == "":
			inList = false
		}
		if !inList {
			continue
		}
		cmd := strings.Fields(line)[0]
		if !commandHelp(&bytes.Buffer{}, cmd) {
			t.Errorf("`logos %s --help` has no entry in `logos help all`", cmd)
		}
	}
}
