package auto

import (
	"slices"
	"testing"
)

// The session-start hook marks itself with LOGOS_HOOK so it never risks its
// timeout on a rebuild. The dream or import it launches has no timeout, and
// inheriting the mark left it answering from an empty index for a whole day.
func TestALaunchedRunIsNotMarkedAsAHook(t *testing.T) {
	env := childEnv([]string{"PATH=/bin", "LOGOS_HOOK=1", "LOGOS_VAULT=/elsewhere"}, "/v")
	if slices.Contains(env, "LOGOS_HOOK=1") {
		t.Errorf("the child is still marked as a hook: %v", env)
	}
	if !slices.Contains(env, "PATH=/bin") || env[len(env)-1] != "LOGOS_VAULT=/v" {
		t.Errorf("the child lost its environment or its vault: %v", env)
	}
}
