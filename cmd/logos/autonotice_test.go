package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/auto"
)

func TestTheUpgradeSaysWhatIsNowOnExactlyOnce(t *testing.T) {
	v := t.TempDir()
	t.Setenv("LOGOS_VAULT", v)
	t.Setenv("LOGOS_HOOK", "")

	var first bytes.Buffer
	noteAutoDefaults(&first, "show", nil)
	for _, want := range []string{"on by default", "dream", "index", "import", "review", "logos config auto off"} {
		if !strings.Contains(first.String(), want) {
			t.Errorf("the notice does not say %q:\n%s", want, first.String())
		}
	}
	if !auto.Said(v, autoDefaultsTopic) {
		t.Fatal("the notice was shown but not recorded in auto.md")
	}
	if b, _ := os.ReadFile(auto.Path(v)); !strings.Contains(string(b), "said: auto-defaults") {
		t.Errorf("auto.md does not hold the marker:\n%s", b)
	}
	var second bytes.Buffer
	noteAutoDefaults(&second, "show", nil)
	if second.Len() != 0 {
		t.Errorf("the notice repeated:\n%s", second.String())
	}
}

func TestTheUpgradeNoticeStaysOutOfJSONHooksAndTheMCPServer(t *testing.T) {
	for name, tc := range map[string]struct {
		cmd  string
		args []string
		hook string
	}{
		"json":       {"show", []string{"--json"}, ""},
		"hook cmd":   {"hook", nil, ""},
		"hook env":   {"show", nil, "1"},
		"mcp server": {"mcp", []string{"serve"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			v := t.TempDir()
			t.Setenv("LOGOS_VAULT", v)
			t.Setenv("LOGOS_HOOK", tc.hook)
			var out bytes.Buffer
			noteAutoDefaults(&out, tc.cmd, tc.args)
			if out.Len() != 0 || auto.Said(v, autoDefaultsTopic) {
				t.Errorf("printed %q or spent the one time it is said", out.String())
			}
		})
	}
}

func TestTheUpgradeNoticeDoesNotListASwitchTheUserTurnedOff(t *testing.T) {
	v := t.TempDir()
	t.Setenv("LOGOS_VAULT", v)
	t.Setenv("LOGOS_HOOK", "")
	if err := os.WriteFile(auto.Path(v), []byte("- dream: off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	noteAutoDefaults(&out, "show", nil)
	if strings.Contains(out.String(), "dream") || !strings.Contains(out.String(), "index") {
		t.Errorf("the notice lists what is on, got:\n%s", out.String())
	}
}
