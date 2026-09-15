package health

import (
	"os"
	"os/exec"
	"testing"

	"github.com/Coder8124/logos/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

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
