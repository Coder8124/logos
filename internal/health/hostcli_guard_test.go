package health

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Coder8124/logos/internal/testenv"
)

func TestMain(m *testing.M) {
	// The vault check looks for history in the default vault under HOME, so a
	// test's empty temp vault on a machine with a real ~/logos was reported as
	// the wrong vault, and passed only where there is none — CI (#211).
	home, err := os.MkdirTemp("", "logos-health-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	os.Unsetenv("LOGOS_VAULT")
	code := testenv.Run(m)
	os.RemoveAll(home)
	os.Exit(code)
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
