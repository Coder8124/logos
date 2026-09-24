package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/testenv"
	"github.com/Coder8124/logos/internal/transcript"
)

// runMainEnv makes the test binary run logos's own main with the arguments it
// holds, separated by \x1f, so a test can watch a whole command line exit.
const runMainEnv = "LOGOS_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	// The developer's own Homebrew logos would be reported by every setup and
	// doctor a test runs; a test that wants one supplies its own prefix.
	health.HomebrewPrefixes = func() []string { return nil }
	// Every resume sweeps transcripts, and the developer's own would be filed
	// into the test's vault whenever a project name matched a folder they
	// worked in. A test that wants a sweep supplies its own transcripts.
	ingest.RecentTranscripts = func(string, time.Time, time.Time, func(string, time.Time) bool) ([]*transcript.Session, []string) {
		return nil, nil
	}
	if args, ok := os.LookupEnv(runMainEnv); ok {
		os.Args = append([]string{"logos"}, strings.Split(args, "\x1f")...)
		main()
		os.Exit(0)
	}
	os.Exit(testenv.Run(m))
}

// A test here that reaches doctor or setup would otherwise run the developer's
// real claude, which starts their real Logos plugin with the test's environment
// and, when that fails, turns Logos off in Claude Code for 15 minutes.
func TestTheseTestsCannotReachARealHostCLI(t *testing.T) {
	for _, name := range testenv.HostCLIs {
		if path, err := exec.LookPath(name); err == nil {
			t.Errorf("%s is reachable at %s during tests", name, path)
		}
	}
}
